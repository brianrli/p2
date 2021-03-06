package main

import (
	"log"
	"os"

	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"sync"

	"github.com/square/p2/pkg/manifest"
	"github.com/square/p2/pkg/pods"
	"github.com/square/p2/pkg/store/consul"
	"github.com/square/p2/pkg/store/consul/flags"
	"github.com/square/p2/pkg/types"
	"github.com/square/p2/pkg/uri"
)

const helpMessage = `
p2-shutdown is a command that is useful to gracefully shutdown pods on a host
before doing maintenance. Ideally, these pods would be relocated to a different
host but we live in a world where hosts are pets.
`

var (
	verbose      = kingpin.Flag("verbose", "Print debugging information").Short('v').Bool()
	dryRun       = kingpin.Flag("dry", "Dry run: do not stop any pods").Short('d').Bool()
	shutdownPods = kingpin.Flag("pods", "The list of pods to shutdown. Leave empty for all").Short('p').Strings()
	podRoot      = kingpin.Flag("pod-root", "The base directory for pods").Default(pods.DefaultPath).String()
)

func main() {
	_, consulOpts, _ := flags.ParseWithConsulOptions()
	client := consul.NewConsulClient(consulOpts)

	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("error getting hostname name: %v", err)
	}

	node := types.NodeName(hostname)
	consulStore := consul.NewConsulStore(client)
	reality, _, err := consulStore.ListPods(consul.REALITY_TREE, node)
	if err != nil {
		log.Fatalf("caught fatal error while querying datastore: %v", err)
	}

	podsToShutdown := make([]types.PodID, 0, len(*shutdownPods))
	for _, pod := range *shutdownPods {
		podsToShutdown = append(podsToShutdown, types.PodID(pod))
	}

	// TODO: configure a proper http client instead of using default fetcher
	podFactory := pods.NewFactory(*podRoot, node, uri.DefaultFetcher)
	var haltWG sync.WaitGroup
	for _, realityEntry := range reality {
		pod := podFactory.NewLegacyPod(realityEntry.Manifest.ID())
		if *dryRun {
			log.Printf("dry run, skipping this pod: %s", pod.Id)
			continue
		}
		if !shutdownPod(pod.Id, podsToShutdown) {
			log.Printf("pod %s not in set of pods to shutdown, skipping", pod.Id)
			continue
		}

		haltWG.Add(1)
		// Halt in the background because Halt() waits for lifecycle scripts
		go func(man manifest.Manifest, podID types.PodID) {
			defer haltWG.Done()
			success, err := pod.Halt(man)
			if !success {
				log.Printf("[ERROR]: at least one launchable of %s did not halt successfully.", podID)
			}
			if err != nil {
				log.Printf("[ERROR]: Got error while halting pod %s. Consider retrying the command. \n %s", podID, err)
			}
		}(realityEntry.Manifest, pod.Id)
	}
	haltWG.Wait()
}

func shutdownPod(podID types.PodID, podsToShutdown []types.PodID) bool {
	if len(podsToShutdown) == 0 {
		return true
	}
	for _, pod := range podsToShutdown {
		if pod == podID {
			return true
		}
	}
	return false
}
