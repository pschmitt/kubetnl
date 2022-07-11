package tunnel

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/inercia/kubetnl/pkg/port"
	"github.com/inercia/kubetnl/pkg/portforward"
)

type TunnelConfig struct {
	genericclioptions.IOStreams

	Namespace        string
	EnforceNamespace bool
	Image            string

	// Name of the tunnel. This will also be the name of the pod and service.
	Name string

	RawPortMappings []string

	PortMappings []port.Mapping

	// The port in the running container that SSH connections are accepted
	// on.
	RemoteSSHPort int

	ContinueOnTunnelError bool

	// The port on the localhost that is used to forward SSH connections to
	// the remote container.
	LocalSSHPort int

	RESTConfig *rest.Config
	ClientSet  *kubernetes.Clientset
}

type Tunnel struct {
	TunnelConfig

	readyCh              chan struct{}
	serviceAccount       *corev1.ServiceAccount
	serviceAccountClient v1.ServiceAccountInterface
	configMap            *corev1.ConfigMap
	configMapClient      v1.ConfigMapInterface
	service              *corev1.Service
	serviceClient        v1.ServiceInterface
	pod                  *corev1.Pod
	podClient            v1.PodInterface
}

func NewTunnel(cfg TunnelConfig) *Tunnel {
	return &Tunnel{
		TunnelConfig: cfg,
		readyCh:      make(chan struct{}), // Closed when portforwarding ready.
	}
}

// Run starts the runnel from the kubernetes cluster to the defined list of port mappings.
func (o *Tunnel) Run(ctx context.Context) (chan struct{}, error) {
	if err := o.CreateService(ctx); err != nil {
		return nil, err
	}

	if err := o.CreateConfigMap(ctx); err != nil {
		return nil, err
	}

	if err := o.CreatePod(ctx); err != nil {
		return nil, err
	}

	kf, err := portforward.NewKubeForwarder(portforward.KubeForwarderConfig{
		PodName:      o.pod.Name,
		PodNamespace: o.pod.Namespace,
		LocalPort:    o.LocalSSHPort,
		RemotePort:   o.RemoteSSHPort,
		RESTConfig:   o.RESTConfig,
		ClientSet:    o.ClientSet,
	})
	if err != nil {
		return nil, err
	}
	if _, err := kf.Run(ctx); err != nil {
		return nil, err
	}

	klog.V(3).Infof("Waiting for SSH port-forward to be ready...")
	select {
	case <-kf.Ready():
		klog.V(3).Infof("SSH port-forward is ready: starting SSH connection...")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	sshtunnel := NewSSHTunnel(o.LocalSSHPort, o.RemoteSSHPort, o.ContinueOnTunnelError)
	if err := sshtunnel.Dial(ctx); err != nil {
		return nil, err
	}
	if err := sshtunnel.RunPortMappings(ctx, o.PortMappings); err != nil {
		return nil, err
	}

	// mark the tunnel as ready
	close(o.readyCh)

	// Note that, in case of a graceful shutdown the defer functions will
	// close the SSH connection, close the portforwarding and cleanup the
	// pod and services.
	return o.readyCh, nil
}

func (o *Tunnel) Ready() <-chan struct{} {
	return o.readyCh
}

func (o *Tunnel) Stop(ctx context.Context) error {
	klog.V(3).Infof("Cleanning up resources in the kubernetes cluster...")

	if err := o.CleanupService(ctx); err != nil {
		return err
	}
	if err := o.CleanupPod(ctx); err != nil {
		return err
	}
	return o.CleanupConfigMap(ctx)
}
