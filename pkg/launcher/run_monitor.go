package launcher

import (
	"context"
	"fmt"

	"github.com/apex/log"
	"github.com/leg100/stok/api/stok.goalspike.com/v1alpha1"
	"github.com/leg100/stok/pkg/k8s"
	"github.com/leg100/stok/pkg/k8s/stokclient"
	"k8s.io/apimachinery/pkg/watch"
	watchtools "k8s.io/client-go/tools/watch"
)

// The runMonitor object watches a specific run obj, passing events to handlers
type runMonitor struct {
	run    *v1alpha1.Run
	client stokclient.Interface
}

func (rm *runMonitor) monitor(ctx context.Context, errch chan<- error) {
	lw := &k8s.RunListWatcher{Client: rm.client, Name: rm.run.GetName(), Namespace: rm.run.GetNamespace()}

	go func() {
		// Should never return unless context cancelled
		if _, err := watchtools.UntilWithSync(ctx, lw, &v1alpha1.Run{}, nil, rm.phaseLogHandler); err != nil {
			errch <- err
		}
	}()
}

// Log run's phase
func (rm *runMonitor) phaseLogHandler(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Deleted:
		return false, fmt.Errorf("run resource deleted")
	}

	switch run := event.Object.(type) {
	case *v1alpha1.Run:
		if phase := run.GetPhase(); string(phase) != "" {
			log.WithField("phase", phase).Debug("Event received")
		}
	}
	return false, nil
}