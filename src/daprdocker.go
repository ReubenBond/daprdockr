package main

import "fmt"
import "strconv"
import "github.com/coreos/go-etcd/etcd"
import "net"
import "strings"
import "encoding/json"
import "errors"
import "os"

const CONTAINER_DOMAIN_SUFFIX = "container"

func main() {
	client := etcd.NewClient([]string{"http://192.168.1.10:5003", "http://192.168.1.10:5002", "http://192.168.1.10:5001"})
	stop := make(chan bool)
	errors := make(chan error)

	go func() {
		for err := range errors {
			fmt.Printf("Error: %s\n", err)
		}
	}()
	go func() {
		for instances := range CurrentInstances(client, stop, &errors) {

			UpdateDns(instances)

			js, err := json.Marshal(instances)
			if err != nil {
				fmt.Printf("Marshalling failed: %s", err)
				return
			}

			fmt.Printf("Update: %s \n\n", js)
		}
		close(errors)
	}()

	go func() {
		for i := 0; i < 12; i++ {
			instance := strconv.Itoa(i)
			service := []string{"web", "db"}[i%2]
			group := "freebay-" + []string{"prod", "ppe", "test"}[i%3]

			response, err := client.Set("instances/"+group+"/"+service+"/"+instance, "127.0.0.1", 50)
			if err != nil {
				fmt.Printf("Error: %s\n", err.Error())
			}
			fmt.Printf("[%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
		stop <- true
	}()

	/*go c.Watch("instances", 0, true, instanceUpdates, stop)
	go func() {
		for i := 0; i < 10; i++ {
			response := <-instanceUpdates
			fmt.Printf("UPDATE [%s] Key: %s Value: %s\n", response.Action, response.Node.Key, response.Node.Value)
		}
		stop <- true
	}()*/

	<-stop
}

func UpdateDns(instances []*Instance) (err error) {
	// Open the hosts file in truncate mode
	hostsFile, err := os.OpenFile("hosts", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return
	}

	defer hostsFile.Close()

	// Update the host file
	for _, instance := range instances {
		for _, entry := range instance.HostEntries() {
			_, err = hostsFile.Write([]byte(entry + "\n"))
			if err != nil {
				return
			}
		}
	}

	// Reload the DNS server
	// TODO: Reload the DNS server

	return
}

type ServiceConfig struct {
	Name      string
	Group     string
	Instances int
}

type Instance struct {
	Group    string
	Service  string
	Instance int
	Addrs    []net.IP
	//PortMappings map[uint16]uint16
}

func (this *Instance) FullyQualifiedDomainName() string {
	return strconv.Itoa(this.Instance) + "." + this.Service + "." + this.Group + "." + CONTAINER_DOMAIN_SUFFIX
}

func (this *Instance) HostEntry(ip net.IP) string {
	return ip.String() + "\t" + this.FullyQualifiedDomainName()
}

func (this *Instance) HostEntries() (entries []string) {
	entries = make([]string, 0, len(this.Addrs))
	for _, ip := range this.Addrs {
		entry := this.HostEntry(ip)
		entries = append(entries, entry)
	}

	return
}

type Operation int

const (
	Add Operation = iota
	Remove
)

type InstanceUpdate struct {
	Operation Operation
	Instance  *Instance
}

// Returns a channel publishing the current instances whenever they change.
func CurrentInstances(client *etcd.Client, stop chan bool, errors *chan error) (currentInstances chan []*Instance) {
	currentInstancesMap := make(map[string]*Instance)
	applyUpdate := func(update *InstanceUpdate) {
		name := update.Instance.FullyQualifiedDomainName()
		switch update.Operation {
		case Add:
			currentInstancesMap[name] = update.Instance
		case Remove:
			delete(currentInstancesMap, name)
		}
		return
	}

	current := func() (instances []*Instance) {
		instances = make([]*Instance, 0, len(currentInstancesMap))

		for _, value := range currentInstancesMap {
			instances = append(instances, value)
		}
		return
	}

	updated := func(update *InstanceUpdate) []*Instance {
		applyUpdate(update)
		return current()
	}

	currentInstances = make(chan []*Instance)
	go func() {
		for update := range InstanceUpdates(client, stop, errors) {
			// Mutate the current instances collection
			currentInstances <- updated(update)
		}
	}()

	return
}

// Returns a channel of all instance updates.
func InstanceUpdates(client *etcd.Client, stop chan bool, errors *chan error) (instances chan *InstanceUpdate) {
	instances = make(chan *InstanceUpdate)
	instanceUpdates := make(chan *etcd.Response)
	go client.Watch("instances", 0, true, instanceUpdates, stop)
	go func() {
		for update := range instanceUpdates {
			instance, err := parseInstanceUpdate(update)
			if err != nil && errors != nil {
				*errors <- err
			} else {
				instances <- instance
			}
		}

		close(instanceUpdates)
	}()
	return
}

func parseActionToOperation(action string) (operation Operation, err error) {
	switch action {
	case "set":
		fallthrough
	case "update":
		fallthrough
	case "create":
		fallthrough
	case "compareAndSwap":
		operation = Add
	case "delete":
		fallthrough
	case "expire":
		operation = Remove
	default:
		err = errors.New("Invalid action: " + action)
	}
	return
}

// Parses an instance from an update response and returns the instance.
func parseInstanceUpdate(update *etcd.Response) (instanceUpdate *InstanceUpdate, err error) {
	keyParts := strings.Split(update.Node.Key, "/")[2:]
	instanceUpdate = new(InstanceUpdate)

	instanceUpdate.Operation, err = parseActionToOperation(update.Action)
	if err != nil {
		return
	}

	instance := new(Instance)

	instance.Group = keyParts[0]
	instance.Service = keyParts[1]
	instance.Instance, err = strconv.Atoi(keyParts[2])
	if err != nil {
		return
	}
	instance.Addrs, err = net.LookupIP(update.Node.Value)
	if err != nil {
		return
	}

	instanceUpdate.Instance = instance
	return
}

/*

config/services/
 ... service definitions ...
 service:
   instances: <num>
   hostPrefix: <string>
   group: <string>
   dockerOptions: [<string>]
   image: <string>
   httpHostName: <string>
   ... https info? ...

instances/<group>/<hostPrefix>/<0..instances>
	<ip address> with 10-second TTL

*/
