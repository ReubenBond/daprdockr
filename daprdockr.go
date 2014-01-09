package main

import (
	"fmt"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	stop := make(chan bool)
	errors := make(chan error)
	go func() {
		for err := range errors {
			if err != nil {
				fmt.Printf("Error: %s\n", err)
			}
		}
	}()

	// TODO: Make this configurable.
	etcdClient := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})
	dockerClient, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		errors <- err
	}

	// Push changes from the local Docker instance into etcd.
	go PushStateChangesIntoStore(dockerClient, etcdClient, stop, &errors)

	// Start a DNS server so that the addresses of service instances can be resolved.
	go ServeDNS(CurrentInstances(etcdClient, stop, &errors), &errors)

	// TODO: Manage local HTTP load balancer config
	// go PushStateChangesIntoHttpLoadBalancer(dockerClient, etcdClient, stop &errors)

	// Pull required state changes from the store and attempt to apply them locally.
	go ApplyRequiredStateChanges(dockerClient, etcdClient, stop, &errors)

	// TODO: remove this.
	// Push in some test data
	go func() {
		for i := 0; i < 12; i++ {
			config := new(ServiceConfig)
			config.Group = "service-prod"
			config.Instances = 5
			config.Name = "web"
			config.Container.Image = "base/devel"
			config.Container.Command = []string{"/bin/sh", "-c", "sleep 100"}
			SetServiceConfig(etcdClient, config)
		}
		return
	}()

	// Spin until killed.
	<-stop
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
forever:
	for {
		select {
		case s := <-sig:
			fmt.Printf("Signal (%d) received, stopping\n", s)
			break forever
		}
	}
}
