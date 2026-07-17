package main

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	// connect to redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	// ping the redis server
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Connected to Redis")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// set a key-value pair
	err := redisClient.Set(ctx, "testCounterKey", 0, 0).Err()
	if err != nil {
		log.Fatalf("Failed to set key-value pair: %v", err)
	}

	for i := 0; i < 10; i++ {
		err = redisClient.Incr(ctx, "testCounterKey").Err()
		if err != nil {
			log.Fatalf("Failed to increment key: %v", err)
		}
		rate, err := redisClient.Get(ctx, "testCounterKey").Int()
		if err != nil {
			log.Fatalf("Failed to get key: %v", err)
		}
		log.Printf("Rate: %d", rate)
	}

	// get un existing key
	rate, err := redisClient.Get(ctx, "unexisting_key").Int()
	if err != nil {
		log.Fatalf("Failed to get key: %v", err)
	}
	log.Printf("Rate: %d", rate)

}