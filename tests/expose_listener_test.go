package test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/phayes/freeport"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	tnet "github.com/fischor/kubetnl/pkg/net"
	"github.com/fischor/kubetnl/pkg/port"
	"github.com/fischor/kubetnl/pkg/portforward"
	"github.com/fischor/kubetnl/pkg/tunnel"
)

// WriteFunc is a function that implements the io.Writer interface.
type WriteFunc func(p []byte) (n int, err error)

func (w WriteFunc) Write(p []byte) (n int, err error) {
	return w(p)
}

// Test that exposes a local service on the kubernetes cluster.
//
// This test creates a local web server and "exposes" this service in a kubernetes cluster with the help
// of a tunnel that forwards the traffic from the kubernetes cluster to the local web server
//
// On the other hand, another tunnel is created for sending HTTP requests to the service in Kubernetes.
//
// In summary, we send HTTP requests to a kubernetes service that sends the request back to us...
//
//     ┌────────────────────────────────────────────────────────────┐
//     │             ┌────────────────────────────┐                 │
//     │             │       ┌──────────────────┐ │                 │
//     │        ┌────► :8080 ├──────────────────┼─┼──────────┐      │
//     │        │    │ :2222 │                  │ │          │      │
//     │        │    │       └──────────────────┘ │          │      │
//     │        │    └────────────────────────────┘          │      │
//     │        │                       Kubernetes cluster   │      │
//     └────────┼────────────────────────────────────────────┼──────┘
//           tunnel                                        tunnel
//     ┌────────┼────────────────────────────────────────────┼──────┐
//     │   ┌────┼────────────────────────────────────────────┼───┐  │
//     │   │  ┌─┴────────────┐  ┌────────────┐  ┌────────────▼─┐ │  │
//     │   │  │              │  │            │  │       :60190 │ │  │
//     │   │  │ HTTP         │  │ Test       │  │              │ │  │
//     │   │  │ Request      │  │ Machinery  │  │              │ │  │
//     │   │  │ Generator    │  │            │  │ Local Web    │ │  │
//     │   │  │              │  │            │  │ Server       │ │  │
//     │   │  └──────────────┘  └────────────┘  └──────────────┘ │  │
//     │   │                   Test Runner                       │  │
//     │   └─────────────────────────────────────────────────────┘  │
//     │                                             Localhost      │
//     └────────────────────────────────────────────────────────────┘
func TestServiceInCluster(t *testing.T) {
	exposeLocalService := features.New("expose local service").
		Assess("expose local service", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			streams := genericclioptions.IOStreams{In: os.Stdin}
			streams.Out = WriteFunc(func(p []byte) (n int, err error) {
				klog.Infof("%s", p)
				return len(p), nil
			})
			streams.ErrOut = WriteFunc(func(p []byte) (n int, err error) {
				klog.Infof("ERROR: %s", p)
				return len(p), nil
			})

			requestReceived := make(chan struct{}, 1)

			klog.Info("Creating a local HTTP server...")
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				klog.Infof("Received a HTTP request: %q: %+v", r.URL.String(), r)

				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Hello, World!"))

				requestReceived <- struct{}{}

				klog.Info("Good! Request received in the local HTTP server.")
			})

			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			klog.Infof("Local HTTP server started at %s", httpServer.URL)
			u, err := url.Parse(httpServer.URL)
			if err != nil {
				t.Fatal(err)
			}

			listenerHost, listenerPortS, _ := net.SplitHostPort(u.Host)
			listenerPort, err := strconv.Atoi(listenerPortS)
			if err != nil {
				t.Fatal(err)
			}

			rest := cfg.Client().RESTConfig()
			cs, err := kubernetes.NewForConfig(rest)
			if err != nil {
				t.Fatal(err)
			}

			kubeToHereConfig := tunnel.TunnelConfig{
				Name:             "kube-8080",
				IOStreams:        streams,
				Image:            tunnel.DefaultTunnelImage,
				Namespace:        cfg.Namespace(),
				EnforceNamespace: true,
				PortMappings: []port.Mapping{
					{
						TargetIP:            listenerHost,
						TargetPortNumber:    listenerPort,
						ContainerPortNumber: 8080,
					},
				},
				ContinueOnTunnelError: true,
				RESTConfig:            rest,
				ClientSet:             cs,
			}

			kubeToHereConfig.LocalSSHPort, err = freeport.GetFreePort()
			if err != nil {
				t.Fatal(err)
			}

			kubeToHereConfig.RemoteSSHPort, err = tnet.GetFreeSSHPortInContainer(kubeToHereConfig.PortMappings)
			if err != nil {
				t.Fatal(err)
			}

			klog.Infof("Creating a tunnel kubernetes[%s:%d]->here:%d",
				kubeToHereConfig.Name,
				kubeToHereConfig.PortMappings[0].ContainerPortNumber,
				kubeToHereConfig.PortMappings[0].TargetPortNumber)

			kubeToHere := tunnel.NewTunnel(kubeToHereConfig)

			hereToKube := portforward.KubeForwarder{
				PodName:      kubeToHere.Name,
				PodNamespace: cfg.Namespace(),
				RemotePort:   8080,
				RESTConfig:   rest,
				ClientSet:    cs,
			}

			hereToKube.LocalPort, err = freeport.GetFreePort()
			if err != nil {
				t.Fatal(err)
			}

			klog.Infof("Creating a tunnel from here:%d->kubernetes[%s:%d]",
				hereToKube.LocalPort,
				hereToKube.PodName,
				kubeToHere.PortMappings[0].ContainerPortNumber)

			klog.Info("Starting both the kube->here and here->kube tunnels")

			klog.Infof("Starting kube->here tunnel...")
			kubeToHereReady, err := kubeToHere.Run(ctx)
			defer func() {
				_ = kubeToHere.Cleanup(context.Background())
			}()
			if err != nil {
				t.Fatal(err)
			}

			klog.Infof("Starting here->kube tunnel...")
			hereToKubeReady, err := hereToKube.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}

			klog.Infof("Waiting until everything is ready for starting tests...")
			<-kubeToHereReady
			<-hereToKubeReady

			klog.Infof("Everything ready: starting tests")
			addr := fmt.Sprintf("http://127.0.0.1:%d/", hereToKube.LocalPort)
			klog.Infof("Checking that we can send a HTTP request to %q", addr)
			response, _ := http.Get(addr)
			if response != nil {
				if response.StatusCode != http.StatusOK {
					t.Fatalf("Response Status code was %d", response.StatusCode)
				}
				b, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				klog.Infof("Good! Received a response to out request: %s", string(b))
			} else {
				t.Fatalf("No response received")
			}

			select {
			case <-requestReceived:
				klog.Info("Test passed SUCCESSFULLY: received the request and the reponse.")
			case <-ctx.Done():
				t.Fatal("Test FAILED: context was canceled")
			}

			return ctx
		}).Feature()

	// test feature
	testenv.Test(t, exposeLocalService)
}
