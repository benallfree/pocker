package pocker

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"pocker/core"
	"strings"
	"syscall"

	"github.com/pluja/pocketbase"
)

// Add this function to get Fly.io machine info
func getFlyMachineInfo() map[string]string {
	return map[string]string{
		"region":     os.Getenv("FLY_REGION"),
		"alloc_id":   os.Getenv("FLY_ALLOC_ID"),
		"app_name":   os.Getenv("FLY_APP_NAME"),
		"machine_id": os.Getenv("FLY_MACHINE_ID"),
		"private_ip": os.Getenv("FLY_PRIVATE_IP"),
	}
}

type Pocker struct {
}

func NewPocker() *Pocker {
	return &Pocker{}
}

func (p *Pocker) Start() error {
	// Add logging of Fly machine info at startup
	if machineInfo := getFlyMachineInfo(); machineInfo["region"] != "" {
		log.Printf("Running on Fly.io - Region: %s, Machine ID: %s, App: %s",
			machineInfo["region"],
			machineInfo["machine_id"],
			machineInfo["app_name"],
			machineInfo["private_ip"])
	}

	pocketbase.NewClient("https://pockethost-central.pockethost.io",
		pocketbase.WithAdminEmailPassword(os.Getenv("MOTHERSHIP_ADMIN_EMAIL"), os.Getenv("MOTHERSHIP_ADMIN_PASSWORD")))
	log.Print("Mothership client authenticated")

	// Add HTTP port flag
	httpAddr := flag.String("http", "0.0.0.0:8080", "the HTTP server address")
	flag.Parse()

	manager := core.NewServerManager()

	// Main server to handle incoming requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request for %s/%s from %s", r.Host, r.URL.Path, r.RemoteAddr)
		// Add basic request logging
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic in request handler: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		// Extract subdomain from host
		parts := strings.Split(r.Host, ".")
		if len(parts) < 3 {
			http.Error(w, "Invalid domain", http.StatusBadRequest)
			return
		}
		subdomain := parts[0]

		log.Printf("Subdomain: %s", subdomain)

		// Get or create PocketBase instance for this subdomain
		instance, err := manager.GetOrCreateInstance(subdomain)
		if err != nil {
			log.Printf("Error creating instance for %s: %v", subdomain, err)
			http.Error(w, "Failed to create instance", http.StatusInternalServerError)
			return
		}

		// Create proxy URL
		targetURL, err := url.Parse(fmt.Sprintf("http://localhost:%d", instance.Port))
		if err != nil {
			log.Printf("Error creating proxy URL for %s: %v", subdomain, err)
			http.Error(w, "Invalid target URL", http.StatusInternalServerError)
			return
		}

		// Create reverse proxy with timeout
		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		// Add timeout and error handling to proxy
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error for %s: %v", subdomain, err)
			http.Error(w, "Proxy Error", http.StatusBadGateway)
		}

		proxy.ServeHTTP(w, r)
	})

	done := make(chan bool)

	// listen for interrupt signal to gracefully shutdown the application
	go func() {
		sigch := make(chan os.Signal, 1)
		signal.Notify(sigch, os.Interrupt, syscall.SIGTERM)
		<-sigch

		done <- true
	}()

	go func() {
		log.Printf("Starting main server on %s", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, nil); err != nil {
			log.Fatal(err)
		}
	}()

	log.Println("Press Ctrl+C to stop")
	<-done
	log.Println("pocketbases exited gracefully")
	return nil
}
