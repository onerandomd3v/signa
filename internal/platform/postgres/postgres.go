package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Connect opens a PostgreSQL connection using the supplied connection URL.
func Connect(ctx context.Context, databaseURL string) (*pgx.Conn, error) {
	return pgx.Connect(ctx, databaseURL)
}

// Ping opens a connection, verifies that PostgreSQL responds, and closes it.
func Ping(ctx context.Context, databaseURL string) error {
	connection, err := Connect(ctx, databaseURL)
	if err != nil {
		return err
	}

	pingErr := connection.Ping(ctx)
	closeErr := connection.Close(context.Background())
	if pingErr != nil {
		return pingErr
	}

	return closeErr
}
