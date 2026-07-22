package db

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

type Mongo struct {
	Pool   *mongo.Database
	conn   *mongo.Client
	config MongoConfig
}

func NewMongo(config MongoConfig) *Mongo {
	return &Mongo{config: config}
}

func (m *Mongo) Connect(ctx context.Context) error {
	uri := fmt.Sprintf(
		"mongodb://%s:%s@%s:%s",
		m.config.Username,
		m.config.Password,
		m.config.Host,
		m.config.Port,
	)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}

	if err := client.Ping(ctx, nil); err != nil {
		return err
	}

	m.Pool = client.Database(m.config.Database)
	m.conn = client

	return nil
}

func (m *Mongo) Begin(ctx context.Context, f func(ctx context.Context, sess *mongo.Session) error) error {
	sess, err := m.conn.StartSession()
	if err != nil {
		return err
	}

	defer sess.EndSession(ctx)

	if err := sess.StartTransaction(); err != nil {
		return err
	}

	if err := f(mongo.NewSessionContext(ctx, sess), sess); err != nil {
		// The abort error is secondary: the transaction failure below is the one
		// the caller must see, and an un-aborted transaction dies with the session.
		_ = sess.AbortTransaction(ctx)
		return err
	}

	return sess.CommitTransaction(ctx)
}

func (m *Mongo) Close(ctx context.Context) error {
	return m.conn.Disconnect(ctx)
}
