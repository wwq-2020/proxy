package proxy

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

type group struct {
	doneCh chan struct{}
}

func newGroup(host string,
	pod *corev1.Pod,
	ports []corev1.ServicePort,
	p *Proxy) *group {
	doneCh := make(chan struct{})
	for _, each := range ports {
		item := newItem(fmt.Sprintf("%s:%d", host, each.Port),
			each.TargetPort.String(), pod,
			p, doneCh)
		go item.proxy()
	}
	return &group{
		doneCh: doneCh,
	}

}

func (g *group) stop() {
	close(g.doneCh)
}
