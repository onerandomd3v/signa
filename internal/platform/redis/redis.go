package redis

import (
	"context"

	goRedis "github.com/redis/go-redis/v9"
)

// Connect creates a Redis client and verifies that the server responds.
func Connect(ctx context.Context, address string) (*goRedis.Client, error) {
	client := goRedis.NewClient(&goRedis.Options{Addr: address})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// Ping verifies that Redis responds at the supplied address.
func Ping(ctx context.Context, address string) error {
	client, err := Connect(ctx, address)
	if err != nil {
		return err
	}
	return client.Close()
}
