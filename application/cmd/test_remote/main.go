package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/neuron-runtime/neuron/application/client"
)

func main() {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	c := client.NewRemote("http://127.0.0.1:8080")
	defer c.Close()

	if err := c.Health(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Remote N.O.R.E. is healthy")
}
