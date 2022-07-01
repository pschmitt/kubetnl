package portforward

import (
	"context"
	"fmt"
	"net/http"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8sportforward "k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/klog/v2"

	"github.com/fischor/kubetnl/pkg/graceful"
)

type KubeForwarder struct {
	PodName string
	PodNamespace string

	LocalPort int
	RemotePort int

	RESTConfig *rest.Config
	ClientSet  *kubernetes.Clientset
}

func (o KubeForwarder) Run(ctx context.Context) error {
	// Setup portforwarding to the pod.
	pfwdReadyCh := make(chan struct{})   // Closed when portforwarding ready.
	pfwdStopCh := make(chan struct{}, 1) // is never closed by k8sportforward
	pfwdDoneCh := make(chan struct{})    // Closed when portforwarding exits.

	go func() error {
		// Do a portforwarding to the pods exposed SSH port.
		req := o.ClientSet.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(o.PodNamespace).
			Name(o.PodName).
			SubResource("portforward")
		transport, upgrader, err := spdy.RoundTripperFor(o.RESTConfig)
		if err != nil {
			return err
		}
		
		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
		pfwdPorts := []string{fmt.Sprintf("%d:%d", o.LocalPort, o.RemotePort)}
		streams := genericclioptions.NewTestIOStreamsDiscard()
		pfwd, err := k8sportforward.New(dialer, pfwdPorts, pfwdStopCh, pfwdReadyCh, streams.Out, streams.ErrOut)
		if err != nil {
			return err
		}
		err = pfwd.ForwardPorts() // blocks
		if err != nil {
			return fmt.Errorf("error port-forwarding from :%d --> %d: %v", o.LocalPort, o.RemotePort, err)
		}

		// If this errors, also everything following will error.
		close(pfwdDoneCh)
		return nil
	}()

	defer graceful.Do(ctx, func() {
		close(pfwdStopCh)
		<-pfwdDoneCh
		klog.V(2).Infof("Cleanup: port-forwarding closed")
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pfwdReadyCh:
		// Note that having a ready pfwd just means that it is listening on LocalPort.
		klog.V(2).Infof("Listening to portforward connections from :%d --> %d", o.LocalPort, o.RemotePort)
	}

	return nil
}
