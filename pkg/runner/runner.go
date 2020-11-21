package runner

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leg100/stok/api/stok.goalspike.com/v1alpha1"
	"github.com/leg100/stok/version"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	cacheVolumeName         = "cache"
	backendConfigVolumeName = "backendconfig"
	credentialsVolumeName   = "credentials"
	ContainerName           = "runner"
	workingDirParent        = "/workspace"
)

func Container(obj controllerutil.Object, ws *v1alpha1.Workspace, attach *v1alpha1.AttachSpec, workingdir, image string) corev1.Container {
	container := corev1.Container{
		Env: []corev1.EnvVar{
			{
				Name:  "STOK_HANDSHAKE",
				Value: strconv.FormatBool(attach.Handshake),
			},
			{
				Name:  "STOK_HANDSHAKE_TIMEOUT",
				Value: attach.HandshakeTimeout,
			},
		},
		Name:                     ContainerName,
		Image:                    image,
		ImagePullPolicy:          corev1.PullIfNotPresent,
		Command:                  []string{"stok", "runner"},
		Stdin:                    true,
		TTY:                      true,
		TerminationMessagePolicy: "FallbackToLogsOnError",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      backendConfigVolumeName,
				MountPath: filepath.Join(workingdir, v1alpha1.BackendTypeFilename),
				SubPath:   v1alpha1.BackendTypeFilename,
				ReadOnly:  true,
			},
			{
				Name:      backendConfigVolumeName,
				MountPath: filepath.Join(workingdir, v1alpha1.BackendConfigFilename),
				SubPath:   v1alpha1.BackendConfigFilename,
				ReadOnly:  true,
			},
			{
				Name:      cacheVolumeName,
				MountPath: filepath.Join(workingdir, ".terraform"),
			},
		},
		WorkingDir: workingdir,
	}

	if ws.Spec.SecretName != "" {
		// Mount secret into a volume and set GOOGLE_APPLICATION_CREDENTIALS to
		// the hardcoded google credentials file (whether it exists or not). Also
		// expose the secret data via environment variables.
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      credentialsVolumeName,
			MountPath: "/credentials",
		})

		//TODO: we set this regardless of whether google credentials exist and that
		//doesn't cause any obvious problems but really should only set it if they exist
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "GOOGLE_APPLICATION_CREDENTIALS",
			Value: "/credentials/google-credentials.json",
		})

		container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ws.Spec.SecretName,
				},
			},
		})
	}

	return container
}

func Pod(obj controllerutil.Object, ws *v1alpha1.Workspace, name, image string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.GetNamespace(),
			Labels: map[string]string{
				// Name of the application
				"app":                    "stok",
				"app.kubernetes.io/name": "stok",

				// Name of higher-level application this app is part of
				"app.kubernetes.io/part-of": "stok",

				// The tool being used to manage the operation of an application
				"app.kubernetes.io/managed-by": "stok-operator",

				// Unique name of instance within application
				"app.kubernetes.io/instance": name,

				// Current version of application
				"version":                   version.Version,
				"app.kubernetes.io/version": version.Version,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: ws.Spec.ServiceAccountName,
			Volumes: []corev1.Volume{
				{
					Name: cacheVolumeName,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: ws.PVCName(),
						},
					},
				},
				{
					Name: backendConfigVolumeName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: v1alpha1.BackendConfigMapName(ws.GetName()),
							},
						},
					},
				},
			},
		},
	}
	if ws.Spec.SecretName != "" {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: credentialsVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ws.Spec.SecretName,
				},
			},
		})
	}

	return pod
}

func WorkspacePod(ws *v1alpha1.Workspace, image string) *corev1.Pod {
	pod := Pod(ws, ws, ws.Name, image)
	pod.SetName(ws.PodName())

	// Component within architecture
	pod.Labels["component"] = "workspace"
	pod.Labels["app.kubernetes.io/component"] = "workspace"

	pod.Spec.InitContainers = []corev1.Container{
		Container(ws, ws, &ws.Spec.AttachSpec, workingDirParent, image),
	}
	pod.Spec.InitContainers[0].Args = append([]string{"--"}, newWorkspaceCommand(ws)...)
	if ws.Spec.Debug {
		pod.Spec.InitContainers[0].Args = append([]string{"--debug"}, pod.Spec.InitContainers[0].Args...)
	}
	// A container that simply idles i.e.  sleeps for infinity, and restarts upon error.
	pod.Spec.Containers = []corev1.Container{
		{
			Name:                     "idler",
			Image:                    image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Command:                  []string{"sh", "-c", "trap \"exit 0\" SIGTERM; while true; do sleep 1; done"},
			TerminationMessagePolicy: "FallbackToLogsOnError",
		},
	}
	return pod
}

func newWorkspaceCommand(cr *v1alpha1.Workspace) []string {
	b := new(strings.Builder)
	b.WriteString("terraform init -backend-config=" + v1alpha1.BackendConfigFilename)
	b.WriteString("; ")
	b.WriteString("terraform workspace select " + cr.GetNamespace() + "-" + cr.GetName())
	b.WriteString(" || ")
	b.WriteString("terraform workspace new " + cr.GetNamespace() + "-" + cr.GetName())
	return []string{"sh", "-c", b.String()}
}

func RunPod(run *v1alpha1.Run, ws *v1alpha1.Workspace, image string) *corev1.Pod {
	pod := Pod(run, ws, run.Name, image)
	pod.SetName(run.Name)

	// Component within architecture
	pod.Labels["component"] = "runner"
	pod.Labels["app.kubernetes.io/component"] = "runner"
	// Workspace that this resource relates to
	pod.Labels["workspace"] = run.Workspace
	pod.Labels["stok.goalspike.com/workspace"] = run.Workspace
	// Run that this resource relates to
	pod.Labels["command"] = run.Command
	pod.Labels["stok.goalspike.com/command"] = run.Command

	workingdir := filepath.Join(workingDirParent, run.ConfigMapPath)
	container := Container(run, ws, &run.AttachSpec, workingdir, image)
	container.Env = append(container.Env, corev1.EnvVar{
		Name:  "TF_WORKSPACE",
		Value: fmt.Sprintf("%s-%s", run.GetNamespace(), ws.GetName()),
	})
	container.Env = append(container.Env, corev1.EnvVar{
		Name:  "STOK_PATH",
		Value: ".",
	})
	container.Env = append(container.Env, corev1.EnvVar{
		Name:  "STOK_TARBALL",
		Value: filepath.Join("/tarball", run.ConfigMapKey),
	})
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "tarball",
		MountPath: filepath.Join("/tarball", run.ConfigMapKey),
		SubPath:   run.ConfigMapKey,
	})

	args := strings.Split(run.Command, " ")
	args = append(args, run.GetArgs()...)
	if run.Command != "sh" {
		args = append([]string{"terraform"}, args...)
	}
	args = append([]string{"--"}, args...)
	if run.Debug {
		args = append([]string{"--debug"}, args...)
	}
	container.Args = args

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "tarball",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: run.ConfigMap,
				},
			},
		},
	})

	pod.Spec.Containers = []corev1.Container{container}
	return pod
}