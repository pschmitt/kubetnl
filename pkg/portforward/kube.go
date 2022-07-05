package portforward

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8sportforward "k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/klog/v2"
)

// KubeForwarder is a portforwarder for forwarding from a local port to a kubernetes Pod and port.
// It is equivalent to "kubectl port-forward".
type KubeForwarder struct {
	PodName      string
	PodNamespace string

	LocalPort  int
	RemotePort int

	RESTConfig *rest.Config
	ClientSet  *kubernetes.Clientset
}

func (o KubeForwarder) Run(ctx context.Context) (chan struct{}, error) {
	// Setup portforwarding to the pod.
	readyCh := make(chan struct{})   // Closed when portforwarding ready.
	stopCh := make(chan struct{}, 1) // is never closed by k8sportforward

	go func() error {
		klog.V(3).Infof("Starting port-forward from :%d --> %s/%s:%d: dialing...", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
		req := o.ClientSet.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(o.PodNamespace).
			Name(o.PodName).
			SubResource("portforward")
		transport, upgrader, err := spdy.RoundTripperFor(o.RESTConfig)
		if err != nil {
			return err
		}

		dialer := spdy.NewDialer(
			upgrader,
			&http.Client{Transport: transport},
			http.MethodPost,
			req.URL())

		pfwdPorts := []string{fmt.Sprintf("%d:%d", o.LocalPort, o.RemotePort)}

		streams := genericclioptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		}

		// loop forever, until the context is canceled.
		for {
			select {
			case <-time.After(500 * time.Millisecond):
				pfwd, err := k8sportforward.New(dialer, pfwdPorts, stopCh, readyCh, streams.Out, streams.ErrOut)
				if err != nil {
					klog.V(3).Infof("error port-forwarding from :%d --> %d: %v", o.LocalPort, o.RemotePort, err)
					continue
				}

				klog.V(3).Infof("Running port-forward from :%d --> %s/%s:%d in a goroutine...", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
				err = pfwd.ForwardPorts() // blocks
				if err != nil {
					klog.V(3).Infof("error port-forwarding from :%d --> %d: %v", o.LocalPort, o.RemotePort, err)
					continue
				}

				klog.V(3).Infof("Port-forward goroutine from :%d --> %s/%s:%d is done.", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
				return nil

			case <-ctx.Done():
				return nil
			}
		}
	}()

	// start a goroutine to wait for the cancellation of the context
	go func() {
		<-ctx.Done()
		klog.V(3).Infof("Context cancelled: stopping port-forward from :%d --> %s/%s:%d.", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
		stopCh <- struct{}{}
	}()

	return readyCh, nil
}
