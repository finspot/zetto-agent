package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type jobConfig struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Input   string `json:"input"`
	Timeout int    `json:"timeout"`
}

type runResult struct {
	Success bool
	Output  string
	Logs    string
}

type jobNotify struct {
	RunID   string `json:"run_id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Logs    string `json:"logs"`
}

func getCommandsList() string {
	listJob := jobConfig{
		ID:      "list",
		Command: "list",
		Input:   "{}",
	}

	res := execJob(listJob)

	if res.Success == false {
		log.Fatal("Could not fetch commands list")
	}

	return res.Output
}

// Poll the API for a job to run
func poll(commands string) (*jobConfig, error) {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Polling from", hostname)

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	payload := fmt.Sprintf("{\"commands\": %s}", commands)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s", os.Getenv("ZETTO_HOST"), "pop"), bytes.NewBufferString(payload))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("ApiKey %s", os.Getenv("ZETTO_API_KEY")))
	req.Header.Add("X-Runner-Name", hostname)
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode == 404 {
		// No error, just not found
		return nil, nil
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("Polling error %d", res.StatusCode)
	}

	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	job := jobConfig{}
	err = decoder.Decode(&job)
	if err != nil {
		log.Fatal(err)
	}

	return &job, nil
}

// Execute a job and returns the runs result
func execJob(job jobConfig) runResult {
	// Prepare command : $RUNNER <command> <input>"
	runner := strings.Split(os.Getenv("ZETTO_RUNNER"), " ")
	runner = append(runner, job.Command)
	runner = append(runner, job.Input)
	cmd := exec.Command(runner[0], runner[1:]...)

	// Collect stdout and stderr into local buffers for after the execution
	outBuf := new(bytes.Buffer)
	logBuf := new(bytes.Buffer)
	cmd.Stdout = outBuf
	cmd.Stderr = logBuf

	// Start the command
	err := cmd.Start()
	if err != nil {
		log.Fatal(err)
	}

	// Create a channel for it to notify its completion (with its exit code)
	done := make(chan int)

	// Asynchronous goroutine
	go func() {
		// Wait for the command to finish
		err := cmd.Wait()
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				// Standard exit error : notify the status through the channel
				done <- exitError.ExitCode()
			} else {
				// Something wrong happened, let's crash
				log.Fatalf("cmd.Wait: %v", err)
			}
		} else {
			// Finished without an error : notify the status zero through the channel
			done <- 0
		}

		// Close the channel, we won't need it anymore
		close(done)
	}()

	// Setup a timer after which the command should be killed
	timeoutDuration := job.Timeout
	if timeoutDuration == 0 {
		timeoutDuration = 15
	}

	timeout := time.NewTimer(time.Duration(timeoutDuration) * time.Second)

	// Prepare a variable into which the exist code will be stored
	var exitCode int

	// Wait simultaneously for an execution end, or the timeout completion
	select {
	case exitCode = <-done:
		// Execution ended, stop the timeout
		if !timeout.Stop() {
			<-timeout.C
		}

	case <-timeout.C:
		// Timeout triggered, kill the process, and return an exit code of 143
		log.Println("Execution timeout, killing process")
		if err := cmd.Process.Kill(); err != nil {
			log.Fatal("failed to kill process: ", err)
		}
		// Wait for the done channel, which should be triggered after the kill. Apparently this emits a -1 exit code
		exitCode = <-done
	}

	// Fetch the command logs through STDERR
	logStr := logBuf.String()

	// Return a failed run if the exit code is not zero
	if exitCode != 0 {
		log.Println("EXIT CODE", exitCode)
		return runResult{
			Success: false,
			Output:  "null",
			Logs:    logStr,
		}
	}

	// Successful run : fetch the output through STDOUT, and return a successful run
	outStr := outBuf.String()
	return runResult{
		Success: true,
		Output:  outStr,
		Logs:    logStr,
	}
}

// Notify the API of a run's result
func notify(job jobConfig, result runResult) error {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	notifyPayload := jobNotify{
		RunID:   job.ID,
		Success: result.Success,
		Output:  result.Output,
		Logs:    result.Logs,
	}

	payload, err := json.Marshal(notifyPayload)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Sending payload %s\n", payload)

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/%s", os.Getenv("ZETTO_HOST"), "notify"), bytes.NewBuffer(payload))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("ApiKey %s", os.Getenv("ZETTO_API_KEY")))
	req.Header.Add("X-Runner-Name", hostname)
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Notify error %d", res.StatusCode)
	}

	return nil
}

func main() {
	log.Print("Started")

	// Check availability of configuration
	if os.Getenv("ZETTO_HOST") == "" {
		log.Fatal("Missing ZETTO_HOST environment")
	}

	if os.Getenv("ZETTO_API_KEY") == "" {
		log.Fatal("Missing ZETTO_API_KEY environment")
	}

	if os.Getenv("ZETTO_RUNNER") == "" {
		log.Fatal("Missing ZETTO_RUNNER environment")
	}

	pollingInterval, err := strconv.Atoi(os.Getenv("ZETTO_POLLING_INTERVAL"))
	if err != nil {
		log.Println("Could not parse env ZETTO_POLLING_INTERVAL, defaulting to 10 seconds")
		pollingInterval = 10
	}

	// TODO : fetch available jobs in order to send them with hre polling request
	commands := getCommandsList()

	// Infinite loop
	for {
		jobconfig, err := poll(commands)

		if err != nil {
			log.Println("Error fetching a job :", err)
			os.Exit(1)
		}

		if jobconfig == nil {
			log.Println("No job found, waiting")
			// Todo : sleep here
			time.Sleep(time.Duration(pollingInterval) * time.Second)
			continue
		}

		runresult := execJob(*jobconfig)

		err = notify(*jobconfig, runresult)

		if err != nil {
			log.Println("Error notifying job result :", err)
			os.Exit(1)
		}
	}
}
