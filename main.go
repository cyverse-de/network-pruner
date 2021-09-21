package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

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

// tojobuuid converts a string containing paths to job files into a job uuid.
func tojobuuid(jobfile string) string {
	return strings.TrimSuffix(path.Base(jobfile), ".json")
}

// convert jobuuid to the default docker network name.
func tonetworkname(jobuuid string) string {
	return fmt.Sprintf("%s_default", strings.Replace(jobuuid, "-", "", -1))
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

// RunningJob contains the information read from the job JSON in a working
// directory. It's compared against the values in a CleanableJob to see if we
// should remove the networks created by the job.
type RunningJob struct {
	InvocationID string `json:"uuid"`
}

// NewRunningJob contains the fields from the job JSON contained in a running
// job's working directory.
func NewRunningJob(filepath string) (*RunningJob, error) {
	rj := &RunningJob{}
	reader, err := os.Open(filepath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open %s", filepath)
	}
	inbytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s", filepath)
	}
	if err = json.Unmarshal(inbytes, rj); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal JSON from %s", filepath)
	}
	return rj, nil
}

// CleanableJob contains the fields from the job JSON that network pruner
// cares about.
type CleanableJob struct {
	InvocationID          string `json:"uuid"`
	LocalWorkingDirectory string `json:"local_working_directory"`
}

// NewCleanableJob initializes a new *CleanableJob from the JSON contained in
// the file at filepath.
func NewCleanableJob(filepath string) (*CleanableJob, error) {
	cl := &CleanableJob{}
	reader, err := os.Open(filepath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open %s", filepath)
	}
	inbytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s", filepath)
	}
	if err = json.Unmarshal(inbytes, cl); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal JSON from %s", filepath)
	}
	return cl, nil
}

func main() {
	var (
		dockerBin       = flag.String("docker", "/usr/bin/docker", "The full path to the docker binary")
		janitorDir      = flag.String("dir", "/opt/image-janitor", "The path to the directory containing job files.")
		numSleepSeconds = flag.String("sleep", "15s", "The number of seconds to sleep for between checks. Needs to be in the Go Duration format.")
		filenameRegex   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
		networkRegex    = regexp.MustCompile(`(?i)^[0-9a-f]{32}_default$`)
	)
	flag.Parse()

	sleepDuration, err := time.ParseDuration(*numSleepSeconds)
	if err != nil {
		log.Fatal(errors.Wrapf(err, "error parsing duration '%s'", *numSleepSeconds))
	}

	for {
		var (
			err                error
			networksFromDocker []string
			removableNetworks  map[string]bool
		)

		removableNetworks = make(map[string]bool)

		// Get the full list of docker networks from docker.
		networksFromDocker, err = listnetworks(*dockerBin)
		if err != nil {
			log.Print(err)
		}

		// By default all docker networks that match the naming convention are
		// considered removable. They'll be toggled back to false later if they
		// shouldn't actually be removed.
		for _, dockernet := range networksFromDocker {
			if networkRegex.MatchString(dockernet) {
				log.Printf("adding %s to the list of removable networks", dockernet)
				removableNetworks[dockernet] = true
			} else {
				removableNetworks[dockernet] = false
			}
		}

		// Get a listing of the job files in the /opt/image-janitor directory.
		jobfiles, err := jobfiles(*janitorDir, filenameRegex)
		if err != nil {
			log.Print(errors.Wrap(err, "failed to get job files"))
		}

		// Add known default networks to the list of networks that can be removed.
		for _, jobfile := range jobfiles {
			var localJob *CleanableJob

			// generate a jobuuid from the local job file.
			uuid := tojobuuid(jobfile)

			// figure out the default network name from the jobuuid
			netname := tonetworkname(uuid)

			// parse the job file to get the local_working_directory.
			localJob, err = NewCleanableJob(jobfile)
			if err != nil {
				log.Print(errors.Wrapf(err, "failed to parse job file %s", jobfile))
				continue
			}

			fmt.Println("")

			// If the condor working directory doesn't exist, then the job is removable
			// because the job is no longer running
			if _, err = os.Open(localJob.LocalWorkingDirectory); err != nil {
				log.Print(fmt.Sprintf("directory %s does not exist, adding %s to remove", localJob.LocalWorkingDirectory, netname))
				removableNetworks[netname] = true
				continue
			}

			// If the job file in the condor working directory exists, we need to make
			// sure the it's not referring to the same job as the local job file. If
			// it does, then the job is still running and the network shouldn't be
			// removed.
			var runningJobFile *os.File
			runningJobFilePath := path.Join(localJob.LocalWorkingDirectory, "job")
			runningJobFile, err = os.Open(runningJobFilePath)
			if err == nil { // the job file in the condor working dir exists
				runningJobFile.Close()

				// parse the job file in the local_working_directory.
				var runningJob *RunningJob
				runningJob, err = NewRunningJob(runningJobFilePath)
				if err != nil {
					log.Print(errors.Wrapf(err, "failed to parse %s, skipping job", runningJobFilePath))
					removableNetworks[netname] = false
					continue
				}

				// if the invocation_ids from the job files match, don't delete the network
				if runningJob.InvocationID == localJob.InvocationID {
					log.Printf("running job %s matches cleanable job %s, skipping clean up\n", runningJob.InvocationID, localJob.InvocationID)
					removableNetworks[netname] = false
					continue
				}
			}
		}

		// At this point, the state is: if the docker network matches the pattern
		// and isn't part of a running job, then it should be marked as true in
		// in the removableNetworks map.
		for dockernet, isremovable := range removableNetworks {
			if isremovable {
				log.Printf("removing docker network %s\n", dockernet)
				if err = removeNetwork(*dockerBin, dockernet); err != nil {
					log.Print(err)
				}
			}
		}

		time.Sleep(sleepDuration)
	}
}
