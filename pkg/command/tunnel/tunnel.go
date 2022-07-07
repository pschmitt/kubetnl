package tunnel

import (
	"context"

	"github.com/phayes/freeport"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/inercia/kubetnl/pkg/graceful"
	"github.com/inercia/kubetnl/pkg/net"
	"github.com/inercia/kubetnl/pkg/port"
	"github.com/inercia/kubetnl/pkg/tunnel"
)

var (
	tunnelShort = "Setup a new tunnel"

	tunnelLong = templates.LongDesc(`
		Setup a new tunnel.

		A tunnel forwards connections directed to a Kubernetes Service port within a
		cluster to an endpoint outside of the cluster, e.g. to your local machine.

		Under the hood "kubetnl tunnel" creates a new service and pod that expose the 
		specified ports. Any incoming connections to an exposed port of the newly created 
		service/pod will be tunneled to the endpoint specified for that port.

		"kubetnl tunnel" runs in the foreground. To stop press CTRL+C once. This will 
		gracefully shutdown all active connections and cleanup the created resources 
		in the cluster before exiting.`)

	tunnelExample = templates.Examples(`
		# Tunnel to local port 8080 from myservice.<namespace>.svc.cluster.local:80.
		kubetnl tunnel myservice 8080:80

		# Tunnel to 10.10.10.10:3333 from myservice.<namespace>.svc.cluster.local:80.
		kubetnl tunnel myservice 10.10.10.10:3333:80

		# Tunnel to local port 8080 from myservice.<namespace>.svc.cluster.local:80 and to local port 9090 from myservice.<namespace>.svc.cluster.local:90.
		kubetnl tunnel myservice 8080:80 9090:90

		# Tunnel to local port 80 from myservice.<namespace>.svc.cluster.local:80 using version 0.1.0 of the kubetnl server image.
		kubetnl tunnel --image docker.io/fischor/kubetnl-server:0.1.0 myservice 80:80`)
)

func NewTunnelCommand(f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	localSSHPort, err := freeport.GetFreePort()
	if err != nil {
		cmdutil.CheckErr(err)
	}

	tunnelConfig := tunnel.TunnelConfig{
		IOStreams:    streams,
		LocalSSHPort: localSSHPort,
		Image:        tunnel.DefaultTunnelImage,
	}

	cmd := &cobra.Command{
		Use:     "tunnel SERVICE_NAME TARGET_ADDR:SERVICE_PORT [...[TARGET_ADDR:SERVICE_PORT]]",
		Short:   tunnelShort,
		Long:    tunnelLong,
		Example: tunnelExample,
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(Complete(&tunnelConfig, f, cmd, args))

			tun := tunnel.NewTunnel(tunnelConfig)

			ctx, cancel := graceful.WithKill(cmd.Context())
			defer cancel()
			ctx, interruptCancel := graceful.WithInterrupt(ctx)
			defer interruptCancel()

			if _, err := tun.Run(ctx); err != nil {
				cmdutil.CheckErr(err)
			}
			defer tun.Stop(context.Background())

			<-tun.Ready()
			<-ctx.Done()
		},
	}

	cmd.Flags().StringVar(&tunnelConfig.Image, "image", tunnelConfig.Image, "The container image thats get deployed to serve a SSH server")

	return cmd
}

func Complete(o *tunnel.TunnelConfig, f cmdutil.Factory, cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return cmdutil.UsageErrorf(cmd, "SERVICE_NAME and list of TARGET_ADDR:SERVICE_PORT pairs are required for tunnel")
	}
	o.Name = args[0]
	var err error
	o.PortMappings, err = port.ParseMappings(args[1:])
	if err != nil {
		return err
	}
	o.RemoteSSHPort, err = net.GetFreeSSHPortInContainer(o.PortMappings)
	if err != nil {
		return err
	}
	o.Namespace, o.EnforceNamespace, err = f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}
	o.RESTConfig, err = f.ToRESTConfig()
	if err != nil {
		return err
	}
	o.ClientSet, err = f.KubernetesClientSet()
	if err != nil {
		return err
	}
	return nil
}
