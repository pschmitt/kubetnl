package portforward

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/phayes/freeport"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8sportforward "k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/klog/v2"
)

// KubeForwarder is a portforwarder for forwarding from a local port to a kubernetes Pod and port.
// It is equivalent to "kubectl port-forward".
type KubeForwarderConfig struct {
	PodName      string
	PodNamespace string

	LocalPort  int
	RemotePort int

	RESTConfig *rest.Config
	ClientSet  *kubernetes.Clientset
}

type KubeForwarder struct {
	sync.Mutex

	KubeForwarderConfig
	readyCh     chan struct{}
	doneCh      chan struct{}
	shouldStop  bool
	stopCh      chan struct{}
	stopChClose sync.Once
}

func NewKubeForwarder(cfg KubeForwarderConfig) (*KubeForwarder, error) {
	var err error
	if cfg.LocalPort == 0 {
		cfg.LocalPort, err = freeport.GetFreePort()
		if err != nil {
			return nil, err
		}
	}

	return &KubeForwarder{
		KubeForwarderConfig: cfg,
		readyCh:             make(chan struct{}),    // Closed when portforwarding ready.
		doneCh:              make(chan struct{}),    // Closed when portforwarding is done.
		stopCh:              make(chan struct{}, 1), // is never closed by k8sportforward
	}, nil
}

func (o *KubeForwarder) Run(ctx context.Context) (chan struct{}, error) {
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
	loop:
		for {
			select {
			case <-time.After(500 * time.Millisecond):
				pfwd, err := k8sportforward.New(dialer, pfwdPorts, o.stopCh, o.readyCh, streams.Out, streams.ErrOut)
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

				o.Lock()
				shouldStop := o.shouldStop
				o.Unlock()

				// check if we are quitting because someone called Stop() or because the port-forward was broken
				// in the last case, loop again
				if shouldStop {
					klog.V(3).Infof("Port-forward from :%d --> %s/%s:%d is done.", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
					break loop
				}
				klog.V(3).Infof("Port-forward from :%d --> %s/%s:%d interrupted: retrying...", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)

			case <-ctx.Done():
				break loop
			}
		}

		close(o.doneCh)
		return nil
	}()

	// start a goroutine to wait for the cancellation of the context
	go func() {
		<-ctx.Done()
		klog.V(3).Infof("Context cancelled: stopping port-forward :%d --> %s/%s:%d.",
			o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)
		o.Stop()
	}()

	return o.readyCh, nil
}

func (o *KubeForwarder) Done() <-chan struct{} {
	return o.doneCh
}

func (o *KubeForwarder) Ready() <-chan struct{} {
	return o.readyCh
}

func (o *KubeForwarder) Stop() error {
	// Make sure we only close the stopCh once.
	o.stopChClose.Do(func() {
		klog.V(3).Infof("Stopping port-forward from :%d --> %s/%s:%d.", o.LocalPort, o.PodNamespace, o.PodName, o.RemotePort)

		o.Lock()
		o.shouldStop = true
		o.Unlock()

		close(o.stopCh)
	})
	return nil
}
