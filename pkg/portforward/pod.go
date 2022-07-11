package portforward

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"
)

func WaitPodReady(ctx context.Context, RESTConfig *rest.Config, namespace, name string) error {
	cs, err := kubernetes.NewForConfig(RESTConfig)
	if err != nil {
		return err
	}

	podClient := cs.CoreV1().Pods(namespace)

	klog.V(3).Infof("Waiting for the Pod to be ready before setting up a SSH connection.")
	watchOptions := metav1.ListOptions{}
	watchOptions.FieldSelector = fields.OneTermEqualSelector("metadata.name", name).String()
	podWatch, err := podClient.Watch(ctx, watchOptions)
	if err != nil {
		return fmt.Errorf("error watching Pod %s: %v", name, err)
	}

	_, err = watchtools.UntilWithoutRetry(ctx, podWatch, condPodReady)
	if err != nil {
		if err == watchtools.ErrWatchClosed {
			return fmt.Errorf("error waiting for Pod ready: podWatch has been closed before pod ready event received")
		}

		// err will be wait.ErrWatchClosed is the context passed to
		// watchtools.UntilWithoutRetry is done. However, if the interrupt
		// context was canceled, return an graceful.Interrupted.
		if ctx.Err() != nil {
			return nil
		}

		if err == wait.ErrWaitTimeout {
			return fmt.Errorf("error waiting for Pod ready: timed out after %d seconds", 300)
		}

		return fmt.Errorf("error waiting for Pod ready: received unknown error \"%f\"", err)
	}

	return nil
}

func condPodReady(event watch.Event) (bool, error) {
	pod := event.Object.(*corev1.Pod)
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			klog.V(3).Infof("Envoy pod check: it is ready !!")
			return true, nil
		}
	}

	klog.V(3).Infof("Envoy pod check: it is NOT ready yet.")
	return false, nil
}
