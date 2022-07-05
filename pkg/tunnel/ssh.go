package tunnel

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/fischor/kubetnl/pkg/graceful"
	"github.com/fischor/kubetnl/pkg/port"
	"github.com/fischor/kubetnl/pkg/portforward"
)

type SSHTunnelForwarderWithListener struct {
	f *portforward.Forwarder
	l net.Listener
}

type SSHTunnel struct {
	LocalSSHPort          int
	RemoteSSHPort         int
	ContinueOnTunnelError bool
	sshClient             *ssh.Client
}

func NewSSHTunnel(localSSHPort, remoteSSHPort int, continueOnTunnelError bool) SSHTunnel {
	return SSHTunnel{
		LocalSSHPort:          localSSHPort,
		RemoteSSHPort:         remoteSSHPort,
		ContinueOnTunnelError: continueOnTunnelError,
	}
}

func (o *SSHTunnel) String() string {
	return fmt.Sprintf(":%d -> :%d", o.LocalSSHPort, o.RemoteSSHPort)
}

func (o *SSHTunnel) Dial(ctx context.Context) error {
	var err error

	// Establish SSH connection over the forwarded port.
	// Retry establishing the connection in case of failure every second.
	sshAddr := fmt.Sprintf("localhost:%d", o.LocalSSHPort)
	klog.V(2).Infof("Establishing SSH connection to %s...", sshAddr)

	sshAttempts := 0
	err = wait.PollImmediateInfinite(time.Second, func() (bool, error) {
		sshAttempts++
		var err error
		o.sshClient, err = sshDialContext(ctx, "tcp", sshAddr, o.sshConfig())
		if err != nil {
			// HACK: net.DialContext does neither return nor wraps
			// the context.Canceled error. Checking if the error
			// was probably caused by a canceled context. See
			// <https://github.com/golang/go/issues/36208>.
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if sshAttempts > 3 {
				klog.V(2).Infof("Failed to dial ssh %q: %v. Retrying...", sshAddr, err)
			}
			klog.V(1).Infof("Error dialing ssh %q: %v", sshAddr, err)
		}
		return err == nil, nil
	})

	if err != nil {
		if err == ctx.Err() {
			klog.V(2).Info("Interrupted while establishing SSH connection")
			return graceful.Interrupted
		}
		// Should not happen since we retry on all errors except for
		// the ctx.Err().
		return fmt.Errorf("error dialing ssh: %v", err)
	}

	return nil
}

func (o *SSHTunnel) Close() error {
	if o.sshClient != nil {
		return o.sshClient.Close()
	}
	return nil
}

// RunPortMappings starts the port forwarding from the SSH tunnel to the destinations
func (o *SSHTunnel) RunPortMappings(ctx context.Context, portMappings []port.Mapping) error {
	var pairs []SSHTunnelForwarderWithListener

	for _, m := range portMappings {
		// TODO: Check for interrupt and ctx.Done in every iteration.
		// TODO Support remote ips: Note that it does not work without the 0.0.0.0 here.
		target := m.TargetAddress()
		remote := fmt.Sprintf("0.0.0.0:%d", m.ContainerPortNumber)
		l, err := o.sshClient.Listen("tcp", remote)
		if err != nil {
			if !o.ContinueOnTunnelError {
				// Close all created listeners.
				for _, p := range pairs {
					p.l.Close()
				}
				klog.V(2).Infof("Failed to tunnel from kube:%d --> %s", m.ContainerPortNumber, target)
				return fmt.Errorf("failed to listen on remote %s: %v", remote, err)
			}
			klog.Errorf("failed to listen on remote %s: %v. No tunnel created.", remote, err)
		}

		pairs = append(pairs,
			SSHTunnelForwarderWithListener{
				f: &portforward.Forwarder{TargetAddr: target},
				l: l,
			})
		klog.V(2).Infof("Tunneling from kube:%d --> %s", m.ContainerPortNumber, target)
	}

	// Open tunnels.
	klog.V(2).Infof("Opening group of tunnels...")
	g, tctx := errgroup.WithContext(ctx)
	for _, pp := range pairs {
		p := pp
		g.Go(func() error {
			klog.V(2).Infof("Starting tunnel ->%s...", p.f)
			defer func() { klog.V(2).Infof("Tunnel ->%s closed.", p.f) }()
			return p.f.Open(p.l)
		})
	}

	closeAll := func() {
		klog.V(2).Infof("Closing all the tunnels...")
		for _, p := range pairs {
			p.f.Close()
		}
		g.Wait()
	}

	go func() {
		select {
		case <-tctx.Done():
			// If tctx is done and tctx.Err is non-nil an error
			// occured. Close the other tunnels if requested.
			// Note that if ctx is done and and tctx.Err is nil,
			// the Errgroup and thus the tunnels already exited.
			if tctx.Err() != nil && !o.ContinueOnTunnelError {
				closeAll()
			}
		case <-ctx.Done():
			closeAll()
		}
	}()

	return nil
}

func (o *SSHTunnel) sshConfig() *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: "user",
		Auth: []ssh.AuthMethod{
			ssh.Password("password"),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			// Accept all keys.
			return nil
		},
	}
}

func sshDialContext(ctx context.Context, network, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: config.Timeout}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}
