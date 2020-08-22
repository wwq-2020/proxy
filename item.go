package proxy

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type item struct {
	service     string
	port        string
	pod         *corev1.Pod
	groupDoneCh chan struct{}
	*Proxy
}

func newItem(service, port string,
	pod *corev1.Pod, proxy *Proxy,
	groupDoneCh chan struct{}) *item {
	return &item{
		service:     service,
		port:        port,
		pod:         pod,
		Proxy:       proxy,
		groupDoneCh: groupDoneCh,
	}
}

func (p *item) proxy() {
	for {
		p.run()
		select {
		case <-p.groupDoneCh:
			return
		default:
		}
		time.Sleep(time.Second)
	}
}

func (p *item) run() {
	pod, err := p.podsLister.Pods(p.pod.Namespace).Get(p.pod.Name)
	if err != nil {
		return
	}

	if pod.Status.Phase != corev1.PodRunning {
		return
	}

	restClient, err := rest.RESTClientFor(p.cfg)
	req := restClient.Post().
		Resource("pods").
		Namespace(pod.Namespace).
		Name(pod.Name).
		SubResource("portforward")
	transport, upgrader, err := spdy.RoundTripperFor(p.cfg)
	if err != nil {
		return
	}

	readyChan := make(chan struct{})

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	fw, err := portforward.NewOnAddresses(dialer, []string{"localhost"}, []string{p.port}, p.groupDoneCh, readyChan, os.Stdout, ioutil.Discard)
	if err != nil {
		return
	}
	doneCh := make(chan struct{})

	go func() {
		select {
		case <-doneCh:
			return
		case <-readyChan:
			ports, err := fw.GetPorts()
			if err != nil {
				return
			}
			p.Lock()
			p.service2Addr[p.service] = fmt.Sprintf(":%d", ports[0].Local)
			p.Unlock()
		}
	}()
	defer func() {
		close(doneCh)
		p.Lock()
		delete(p.service2Addr, p.service)
		p.Unlock()
	}()
	if err := fw.ForwardPorts(); err != nil {
		return
	}
}
