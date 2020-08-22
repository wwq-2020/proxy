package proxy

import (
	"net"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	clientCorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/scheme"
)

// Proxy Proxy
type Proxy struct {
	cfg       *rest.Config
	clientSet *kubernetes.Clientset
	stopCh    chan struct{}
	sync.Mutex
	service2Addr    map[string]string
	podsLister      clientCorev1.PodLister
	servicesLister  clientCorev1.ServiceLister
	informerFactory informers.SharedInformerFactory
	host2Pods       map[string][]*corev1.Pod
	pod2Group       map[string]*group
	pod2Service     map[string]*corev1.Service
	pod2ProxyItem   map[string]*item
}

// Dial Dial
func (p *Proxy) Dial(network, address string) (net.Conn, error) {
	p.Lock()
	defer p.Unlock()
	item, exist := p.service2Addr[address]
	if exist {
		address = item
	}
	return (&net.Dialer{}).Dial(network, address)
}

// Run Run
func (p *Proxy) Run() {
	p.informerFactory.WaitForCacheSync(p.stopCh)
}

func (p *Proxy) onServiceAdd(obj interface{}) {
	p.Lock()
	defer p.Unlock()
	service, ok := obj.(*corev1.Service)
	if !ok {
		return
	}
	if len(service.Spec.Ports) == 0 {
		return
	}
	p.startProxyTo(service)
}

func (p *Proxy) startProxyTo(service *corev1.Service) {
	selector := labels.SelectorFromSet(labels.Set(service.Labels))
	pods, err := p.podsLister.List(selector)
	if err != nil {
		return
	}
	if len(pods) == 0 {
		return
	}
	pod := pods[0]

	host := buildHost(service)
	g := newGroup(host, pod, service.Spec.Ports, p)
	p.host2Pods[host] = pods
	p.pod2Group[host] = g
	p.pod2Service[pod.Name] = service
}

func (p *Proxy) stopProxyTo(service *corev1.Service) {
	host := buildHost(service)
	for _, each := range p.host2Pods[host] {
		delete(p.pod2Service, each.Name)
		if group, exist := p.pod2Group[each.Name]; exist {
			group.stop()
			delete(p.pod2Group, each.Name)
		}
	}
	delete(p.host2Pods, host)
}

func (p *Proxy) reProxyTo(service *corev1.Service) {
	p.stopProxyTo(service)
	p.startProxyTo(service)
}

func (p *Proxy) onServiceUpdate(oldObj, newObj interface{}) {
	p.Lock()
	defer p.Unlock()
	oldService, ok := oldObj.(*corev1.Service)
	if !ok {
		return
	}
	newService, ok := newObj.(*corev1.Service)
	if !ok {
		return
	}

	p.stopProxyTo(oldService)
	p.startProxyTo(newService)
}

func (p *Proxy) onServiceDelete(obj interface{}) {
	p.Lock()
	defer p.Unlock()
	service, ok := obj.(*corev1.Service)
	if !ok {
		return
	}
	p.stopProxyTo(service)
}

func buildHost(service *corev1.Service) string {
	return strings.Join([]string{service.Name, service.Namespace, "svc", "cluster", "local"}, ".")
}

func (p *Proxy) onPodDelete(obj interface{}) {
	p.Lock()
	defer p.Unlock()
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	service, exist := p.pod2Service[pod.Name]
	if !exist {
		return
	}
	p.reProxyTo(service)
}

// New New
func New(kubeconfigPath string) *Proxy {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		panic(err)
	}
	setKubernetesDefaults(cfg)
	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	informerFactory := informers.NewSharedInformerFactory(clientSet, time.Minute)
	pods := informerFactory.Core().V1().Pods()
	podsLister := pods.Lister()
	services := informerFactory.Core().V1().Services()
	servicesLister := services.Lister()

	proxy := &Proxy{
		clientSet:       clientSet,
		stopCh:          make(chan struct{}),
		cfg:             cfg,
		service2Addr:    make(map[string]string),
		podsLister:      podsLister,
		servicesLister:  servicesLister,
		informerFactory: informerFactory,
		host2Pods:       make(map[string][]*corev1.Pod),
		pod2Service:     make(map[string]*corev1.Service),
		pod2ProxyItem:   make(map[string]*item),
	}

	podsInformer := pods.Informer()

	servicesInformer := services.Informer()
	servicesInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    proxy.onServiceAdd,
			UpdateFunc: proxy.onServiceUpdate,
			DeleteFunc: proxy.onServiceDelete,
		})
	podsInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			DeleteFunc: proxy.onPodDelete,
		})
	informerFactory.Start(proxy.stopCh)
	return proxy
}

// Stop Stop
func (p *Proxy) Stop() {
	close(p.stopCh)
}

func setKubernetesDefaults(config *rest.Config) error {
	config.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}

	if config.APIPath == "" {
		config.APIPath = "/api"
	}
	if config.NegotiatedSerializer == nil {
		config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	}
	return rest.SetKubernetesDefaults(config)
}
