//this need to to connnect to the mino.io server to store audio files
// // The server can be deployed on cloud platforms such as AWS, GCP, or Azure.
// // The server can be monitored using tools like Prometheus and Grafana.
// make the connection to the mino.io server

package main

import (
	"fmt"
	"net/http"
)

func main() {
	// Set up a simple HTTP server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, Content Service!")
	})

	// Start the server on port 8081
	fmt.Println("Content service is running on port 8081...")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		fmt.Println("Error starting server:", err)
	}
}