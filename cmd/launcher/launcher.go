package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/leg100/etok/pkg/builders"
	"github.com/leg100/etok/pkg/commands"
	etokerrors "github.com/leg100/etok/pkg/errors"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/leg100/etok/api/etok.dev/v1alpha1"
	"github.com/leg100/etok/cmd/flags"
	cmdutil "github.com/leg100/etok/cmd/util"
	"github.com/leg100/etok/pkg/archive"
	"github.com/leg100/etok/pkg/client"
	"github.com/leg100/etok/pkg/env"
	"github.com/leg100/etok/pkg/globals"
	"github.com/leg100/etok/pkg/handlers"
	"github.com/leg100/etok/pkg/k8s"
	"github.com/leg100/etok/pkg/logstreamer"
	"github.com/leg100/etok/pkg/monitors"
	"github.com/leg100/etok/pkg/repo"
	"github.com/leg100/etok/pkg/util"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/util/term"
)

const (
	defaultWorkspace        = "default"
	defaultReconcileTimeout = 10 * time.Second

	// default namespace runs are created in
	defaultNamespace  = "default"
	defaultPodTimeout = time.Hour
)

var (
	errNotAuthorised     = errors.New("you are not authorised")
	errWorkspaceNotFound = errors.New("workspace not found")
	errWorkspaceNotReady = errors.New("workspace not ready")
	errReconcileTimeout  = errors.New("timed out waiting for run to be reconciled")
)

// launcherOptions deploys a new Run. It monitors not only its progress, but
// that of its pod and its workspace too. It stream logs from the pod to the
// client, or, if a TTY is detected on the client, it attaches the client to the
// pod's TTY, permitting input/output. It then awaits the completion of the pod,
// reporting its container's exit code.
type launcherOptions struct {
	*cmdutil.Factory

	args []string

	*client.Client

	path        string
	namespace   string
	workspace   string
	kubeContext string
	runName     string

	// Etok run command
	command commands.Command
	// etok Workspace's workspaceSpec
	workspaceSpec v1alpha1.WorkspaceSpec
	// Create a service acccount if it does not exist
	disableCreateServiceAccount bool
	// Create a secret if it does not exist
	disableCreateSecret bool

	// Disable default behaviour of deleting resources upon error
	disableResourceCleanup bool

	// Timeout for wait for handshake
	handshakeTimeout time.Duration
	// Timeout for run pod to be running and ready
	podTimeout time.Duration
	// Timeout for resource to be reconciled (at least once)
	reconcileTimeout time.Duration

	// Disable TTY detection
	disableTTY bool

	// Recall if resources are created so that if error occurs they can be cleaned up
	createdRun     bool
	createdArchive bool

	// For testing purposes set run status
	status *v1alpha1.RunStatus

	// attach toggles whether pod will be attached to (true), or streamed from
	// (false)
	attach bool

	// Git repo from which run is being launched
	repo *repo.Repo
}

func launcherCommand(f *cmdutil.Factory, o *launcherOptions) *cobra.Command {
	o.Factory = f
	o.namespace = defaultNamespace

	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [flags] -- [%[1]s args]", o.command.Child()),
		Short: o.command.GetShortDesc(),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			o.args = args

			// Tests override run name
			if o.runName == "" {
				o.runName = fmt.Sprintf("run-%s", util.GenerateRandomString(5))
			}

			// Toggle whether to attach to pod's TTY
			o.attach = !o.disableTTY && term.IsTerminal(o.In)

			// Ensure path is within a git repository
			o.repo, err = repo.Open(o.path)
			if err != nil {
				return err
			}

			o.Client, err = f.Create(o.kubeContext)
			if err != nil {
				return err
			}

			// Override namespace and workspace from env file values
			envFile, err := lookupEnvFile(o.path)
			if err != nil {
				return err
			}
			if envFile != nil {
				if !flags.IsFlagPassed(cmd.Flags(), "namespace") {
					o.namespace = envFile.Namespace
				}
				if !flags.IsFlagPassed(cmd.Flags(), "workspace") {
					o.workspace = envFile.Workspace
				}
			}

			err = o.run(cmd.Context())
			if err != nil {
				// Cleanup resources upon error. An exit code error means the
				// runner ran successfully but the program it executed failed
				// with a non-zero exit code. In this case, resources are not
				// cleaned up.
				var exit etokerrors.ExitError
				if !errors.As(err, &exit) {
					if !o.disableResourceCleanup {
						o.cleanup()
					}
				}
			}
			return err
		},
	}

	flags.AddPathFlag(cmd, &o.path)
	flags.AddNamespaceFlag(cmd, &o.namespace)
	flags.AddWorkspaceFlag(cmd, &o.workspace)
	flags.AddKubeContextFlag(cmd, &o.kubeContext)
	flags.AddDisableResourceCleanupFlag(cmd, &o.disableResourceCleanup)

	cmd.Flags().BoolVar(&o.disableTTY, "no-tty", false, "disable tty")
	cmd.Flags().DurationVar(&o.podTimeout, "pod-timeout", defaultPodTimeout, "timeout for pod to be ready and running")
	cmd.Flags().DurationVar(&o.handshakeTimeout, "handshake-timeout", v1alpha1.DefaultHandshakeTimeout, "timeout waiting for handshake")

	cmd.Flags().DurationVar(&o.reconcileTimeout, "reconcile-timeout", defaultReconcileTimeout, "timeout for resource to be reconciled")

	return cmd
}

func lookupEnvFile(path string) (*env.Env, error) {
	etokenv, err := env.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			// A missing env file is OK
			return nil, nil
		}
		return nil, err
	}
	return etokenv, nil
}

func (o *launcherOptions) run(ctx context.Context) error {
	// Tar up local config and deploy k8s resources
	run, err := o.deploy(ctx)
	if err != nil {
		return err
	}

	if o.command.Queueable {
		// Watch and log queue updates
		o.watchQueue(ctx, run)
	}

	g, gctx := errgroup.WithContext(ctx)

	// Wait for resource to have been successfully reconciled at least once
	// within the ReconcileTimeout (If we don't do this and the operator is
	// either not installed or malfunctioning then the user would be none the
	// wiser until the much longer PodTimeout had expired).
	g.Go(func() error {
		return o.waitForReconcile(gctx, run)
	})

	// Wait for run to indicate pod is running
	g.Go(func() error {
		return o.watchRun(gctx, run)
	})

	// Check workspace exists and is healthy
	if err := o.checkWorkspace(ctx, run); err != nil {
		return err
	}

	// Carry on waiting for run to indicate pod is ready
	if err := g.Wait(); err != nil {
		return err
	}

	// Watch the run for the container's exit code. Non-blocking.
	exit := monitors.RunExitMonitor(ctx, o.EtokClient, o.namespace, o.runName)

	// Connect to pod
	if o.attach {
		if err := o.AttachFunc(o.Out, *o.Config, o.namespace, o.runName, o.In.(*os.File), cmdutil.HandshakeString, globals.RunnerContainerName); err != nil {
			return err
		}
	} else {
		if err := logstreamer.Stream(ctx, o.GetLogsFunc, o.Out, o.PodsClient(o.namespace), o.runName, globals.RunnerContainerName); err != nil {
			return err
		}
	}

	// Await container's exit code
	select {
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timed out waiting for exit code")
	case code := <-exit:
		if code != nil {
			return code
		}
	}

	if o.command.UpdatesLockFile {
		// Some commands (e.g. terraform init) update the lock file,
		// .terraform.lock.hcl, and it's recommended that this be committed to
		// version control. So the runner copies it to a config map, and it is
		// here that that config map is retrieved.
		lock, err := o.ConfigMapsClient(o.namespace).Get(ctx, v1alpha1.RunLockFileConfigMapName(run.Name), metav1.GetOptions{})
		if kerrors.IsNotFound(err) {
			fmt.Fprintf(o.Out, "%s %s was not persisted to a config map\n", color.YellowString("Warning:"), globals.LockFile)
			return nil
		} else if err != nil {
			return fmt.Errorf("unable to retrieve %s from configmap %s: %w", globals.LockFile, v1alpha1.RunLockFileConfigMapName(run.Name), err)
		}

		// Write lock file to user's disk
		lockFilePath := filepath.Join(o.path, globals.LockFile)
		if err := os.WriteFile(lockFilePath, lock.BinaryData[globals.LockFile], 0644); err != nil {
			return err
		}

		klog.V(1).Infof("Written %s", lockFilePath)
	}

	return nil
}

func (o *launcherOptions) watchRun(ctx context.Context, run *v1alpha1.Run) error {
	lw := &k8s.RunListWatcher{Client: o.EtokClient, Name: run.Name, Namespace: run.Namespace}
	_, err := watchtools.UntilWithSync(ctx, lw, &v1alpha1.Run{}, nil, handlers.RunConnectable(run.Name, o.attach))
	return err
}

func (o *launcherOptions) watchQueue(ctx context.Context, run *v1alpha1.Run) {
	go func() {
		lw := &k8s.WorkspaceListWatcher{Client: o.EtokClient, Name: o.workspace, Namespace: o.namespace}
		// Ignore errors TODO: the current logger has no warning level. We
		// should probably upgrade the logger to something that does, and then
		// log any error here as a warning.
		_, _ = watchtools.UntilWithSync(ctx, lw, &v1alpha1.Workspace{}, nil, handlers.LogQueuePosition(run.Name))
	}()
}

func (o *launcherOptions) checkWorkspace(ctx context.Context, run *v1alpha1.Run) error {
	ws, err := o.WorkspacesClient(o.namespace).Get(ctx, o.workspace, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		return fmt.Errorf("%w: %s/%s", errWorkspaceNotFound, o.namespace, o.workspace)
	}
	if err != nil {
		return err
	}

	// ...ensure workspace is ready
	workspaceReady := meta.FindStatusCondition(ws.Status.Conditions, v1alpha1.WorkspaceReadyCondition)
	if workspaceReady == nil {
		return fmt.Errorf("%w: %s: ready condition not found", errWorkspaceNotReady, klog.KObj(ws))
	}
	if workspaceReady.Status != metav1.ConditionTrue {
		return fmt.Errorf("%w: %s: %s", errWorkspaceNotReady, klog.KObj(ws), workspaceReady.Message)
	}

	// ...approve run if command listed as privileged
	if ws.IsPrivilegedCommand(o.command.Path) {
		if err := o.approveRun(ctx, ws, run); err != nil {
			return err
		}
	}

	return nil
}

// Deploy ConfigMap and Run resources in parallel
func (o *launcherOptions) deploy(ctx context.Context) (run *v1alpha1.Run, err error) {
	g, ctx := errgroup.WithContext(ctx)

	// Construct ConfigMap containing tarball of local terraform modules, and
	// deploy
	g.Go(func() error {
		configMap, err := archive.ConfigMap(o.namespace, o.runName, o.path, o.repo.Root())
		if err != nil {
			return err
		}

		_, err = o.ConfigMapsClient(o.namespace).Create(ctx, configMap, metav1.CreateOptions{})
		if err != nil {
			return err
		}

		o.createdArchive = true
		klog.V(1).Infof("created config map %s\n", klog.KObj(configMap))

		return nil
	})

	// Construct and deploy command resource
	g.Go(func() error {
		run, err = o.createRun(ctx, o.runName)
		return err
	})

	return run, g.Wait()
}

func (o *launcherOptions) cleanup() {
	if o.createdRun {
		o.RunsClient(o.namespace).Delete(context.Background(), o.runName, metav1.DeleteOptions{})
	}
	if o.createdArchive {
		o.ConfigMapsClient(o.namespace).Delete(context.Background(), o.runName, metav1.DeleteOptions{})
	}
}

func (o *launcherOptions) approveRun(ctx context.Context, ws *v1alpha1.Workspace, run *v1alpha1.Run) error {
	klog.V(1).Infof("%v is a privileged command on workspace\n", o.command)
	annotations := ws.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[run.ApprovedAnnotationKey()] = "approved"
	ws.SetAnnotations(annotations)

	_, err := o.WorkspacesClient(o.namespace).Update(ctx, ws, metav1.UpdateOptions{})
	if err != nil {
		if kerrors.IsForbidden(err) {
			return fmt.Errorf("attempted to run privileged command %v: %w", o.command, errNotAuthorised)
		} else {
			return fmt.Errorf("failed to update workspace to approve privileged command: %w", err)
		}
	}
	klog.V(1).Info("successfully approved run with workspace")

	return nil
}

// Construct and deploy command resource
func (o *launcherOptions) createRun(ctx context.Context, name string) (*v1alpha1.Run, error) {
	bldr := builders.Run(o.namespace, name, o.workspace, o.command.Path, o.args...)

	bldr.SetVerbosity(o.Verbosity)

	if o.status != nil {
		// For testing purposes seed status
		bldr.SetStatus(*o.status)
	}

	if o.attach {
		bldr.Attach()
	}

	run, err := o.RunsClient(o.namespace).Create(ctx, bldr.Build(), metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	o.createdRun = true
	klog.V(1).Infof("created run %s\n", klog.KObj(run))

	return run, nil
}

// waitForReconcile waits for the run resource to be reconciled.
func (o *launcherOptions) waitForReconcile(ctx context.Context, run *v1alpha1.Run) error {
	lw := &k8s.RunListWatcher{Client: o.EtokClient, Name: run.Name, Namespace: run.Namespace}
	hdlr := handlers.Reconciled(run)

	ctx, cancel := context.WithTimeout(ctx, o.reconcileTimeout)
	defer cancel()

	_, err := watchtools.UntilWithSync(ctx, lw, &v1alpha1.Run{}, nil, hdlr)
	if err != nil {
		if errors.Is(err, wait.ErrWaitTimeout) {
			return errReconcileTimeout
		}
		return err
	}
	return nil
}
