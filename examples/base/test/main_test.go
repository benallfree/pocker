package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func makeRequest(host string, url string, wg *sync.WaitGroup) {
	defer wg.Done()

	start := time.Now()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("URL: %s, Error: %v, Duration: %v\n", url, err, duration)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("URL: %s, Status: %d, Duration: %v\n", url, resp.StatusCode, duration)
}

func main() {
	maxSubdomains := flag.Int("max", 500, "number of different subdomains to hit")
	concurrency := flag.Int("concurrent", 50, "number of concurrent requests")
	flag.Parse()

	// Create a channel to control concurrency
	semaphore := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup

	// Setup signal handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Create a done channel to signal goroutines to stop
	done := make(chan struct{})

	// Start the request loop in a separate goroutine
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				subdomain := fmt.Sprintf("%08d", rand.Intn(*maxSubdomains))
				url := "http://localhost/api/health"

				wg.Add(1)
				semaphore <- struct{}{} // Acquire semaphore

				go func(url string) {
					makeRequest(fmt.Sprintf("%s.lvh.me", subdomain), url, &wg)
					<-semaphore // Release semaphore
				}(url)
			}
		}
	}()

	// Wait for interrupt signal
	<-stop
	fmt.Println("\nShutting down gracefully...")
	close(done)
}
