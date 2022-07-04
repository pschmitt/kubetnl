package net

import (
	"fmt"

	"github.com/fischor/kubetnl/pkg/port"
)

// GetFreeSSHPortInContainer chooses the port number for the SSH server respecting the ports
// that are used for incoming traffic.
func GetFreeSSHPortInContainer(mm []port.Mapping) (int, error) {
	if !isInUse(mm, 2222) {
		return 2222, nil
	}
	// TODO: for 22 portforwarding somewhat never works.
	if !isInUse(mm, 22) {
		return 22, nil
	}
	min := 49152
	max := 65535
	for i := min; i <= max; i++ {
		if !isInUse(mm, i) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("Failed to choose a port for the SSH connection - all ports in use")
}

func isInUse(mm []port.Mapping, containerPort int) bool {
	for _, m := range mm {
		if m.ContainerPortNumber == containerPort {
			return true
		}
	}
	return false
}
