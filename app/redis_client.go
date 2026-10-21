package main

import (
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisClient is the shared L2 cache client.
var redisClient *redis.Client

func connectRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("REDIS_URL: %v", err)
	}
	options.DialTimeout = 5 * time.Second // avoid hanging

	redisClient = redis.NewClient(options)
}
