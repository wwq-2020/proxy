package proxy

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path"
	"testing"
	"time"
)

func TestProxy(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get homedir:%#v", err)
	}
	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)
	proxy := New(path.Join(homeDir, ".kube", "config"))
	proxy.Run()
	defer proxy.Stop()

	c := http.Client{
		Transport: &http.Transport{
			Dial: proxy.Dial,
		},
	}
	time.Sleep(time.Second * 5)
	resp, err := c.Get("http://webdemo")
	if err != nil {
		panic(err)
	}
	data, err := ioutil.ReadAll(resp.Body)
	fmt.Println(string(data), err)
	<-ch
}
