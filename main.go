package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

func jobfiles(dir string, fnameregex *regexp.Regexp) ([]string, error) {
	var retval []string

	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.Mode().IsRegular() {
			if !fnameregex.Match([]byte(entry.Name())) {
				continue
			}
			retval = append(retval, path.Join(dir, entry.Name()))
		}
	}

	return retval, nil
}

// tojobuuids converts a []string containing paths to job files into a list of job
// uuids.
func tojobuuids(jobfiles []string) []string {
	var retval []string

	for _, j := range jobfiles {
		retval = append(retval, strings.TrimSuffix(path.Base(j), ".json"))
	}

	return retval
}

// Returns a listing of jobuuids from the image-janitor directory.
func jobuuids(dir string, fnameregex *regexp.Regexp) ([]string, error) {
	fpaths, err := jobfiles(dir, fnameregex)
	if err != nil {
		return nil, errors.Wrap(err, "error getting list of job files")
	}
	return tojobuuids(fpaths), nil
}

// convert jobuuid to the default docker network name.
func tonetworkname(jobuuid string) string {
	return fmt.Sprintf("%s_default", strings.Replace(jobuuid, "-", "", -1))
}

// defaultnetworks returns a listing of default docker networks that might be
// on the box based on the listing of files in the image-janitor directory.
func defaultnetworks(dir string, fnameregex *regexp.Regexp) ([]string, error) {
	uuids, err := jobuuids(dir, fnameregex)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get list of job uuids")
	}
	var retval []string
	for _, uuid := range uuids {
		retval = append(retval, tonetworkname(uuid))
	}
	return retval, nil
}

// listnetworks returns a list of the networks on the host based on the output
// of the docker network ls command.
func listnetworks(dockerBin string) ([]string, error) {
	var err error
	listing := bytes.NewBuffer(make([]byte, 0))

	netCmd := exec.Command(dockerBin, "network", "ls", "--format", "{{ .Name }}", "-q")
	netCmd.Env = os.Environ()
	netCmd.Stdout = listing
	netCmd.Stderr = os.Stderr

	if err = netCmd.Run(); err != nil {
		return nil, err
	}

	unparsed := bytes.Split(listing.Bytes(), []byte{'\n'})

	var retval []string
	for _, b := range unparsed {
		retval = append(retval, string(b))
	}

	return retval, nil
}

func removeNetwork(dockerBin, netName string) error {
	rmCmd := exec.Command(dockerBin, "network", "rm", netName)
	rmCmd.Env = os.Environ()
	rmCmd.Stdout = os.Stdout
	rmCmd.Stderr = os.Stderr
	return rmCmd.Run()
}

func main() {
	var (
		err           error
		dockerBin     = flag.String("docker", "/usr/bin/docker", "The full path to the docker binary")
		janitorDir    = flag.String("dir", "/opt/image-janitor", "The path to the directory containing job files.")
		filenameRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
	)
	flag.Parse()

	networksFromDocker, err := listnetworks(*dockerBin)
	if err != nil {
		log.Print(err)
	}
	networksFromJobs, err := defaultnetworks(*janitorDir, filenameRegex)
	if err != nil {
		log.Print(err)
	}

	// Only try to prune a job if it was created for a job and that job is no
	// longer running.
	for _, dockernet := range networksFromDocker {
		log.Printf("found docker network %s\n", dockernet)
		found := false
		for _, jobnet := range networksFromJobs {
			if dockernet == jobnet {
				found = true
				log.Printf("docker network %s is a job network\n", jobnet)
			}
		}
		if found {
			log.Printf("removing docker network %s\n", dockernet)
			if err = removeNetwork(*dockerBin, dockernet); err != nil {
				log.Print(err)
			}
		}
	}
}
