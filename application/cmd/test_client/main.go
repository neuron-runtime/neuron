package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/neuron-runtime/neuron/application/client"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := client.NewDefaultLocal()
	defer c.Close()

	if err := c.Health(ctx); err != nil {
		log.Fatalf("N.O.R.E. is not healthy: %v", err.Error())
	}

	instances, err := c.ListInstances(ctx, "")
	if err != nil {
		fmt.Printf("N.O.R.E instance error %v\n", err)
	}

	fmt.Printf("N.O.R.E instances %v\n", instances)

	fmt.Println("N.O.R.E. is healthy")
}
