package tunnel

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/fischor/kubetnl/pkg/port"
)

func protocolToCoreV1(p port.Protocol) corev1.Protocol {
	if p == port.ProtocolSCTP {
		return corev1.ProtocolSCTP
	}
	if p == port.ProtocolUDP {
		return corev1.ProtocolUDP
	}
	return corev1.ProtocolTCP
}
