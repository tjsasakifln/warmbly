//go:build !minprofile

package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client wraps Google Pub/Sub for real-time streaming
type Client struct {
	client    *pubsub.Client
	projectID string
}

// NewClient creates a new Pub/Sub client
func NewClient(ctx context.Context, projectID string) (*Client, error) {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	return &Client{
		client:    client,
		projectID: projectID,
	}, nil
}

// Close closes the Pub/Sub client
func (c *Client) Close() error {
	return c.client.Close()
}

// getTopic gets or creates a topic
func (c *Client) getTopic(ctx context.Context, topicID string) (*pubsub.Topic, error) {
	topic := c.client.Topic(topicID)
	exists, err := topic.Exists(ctx)
	if err != nil {
		return nil, err
	}

	if !exists {
		topic, err = c.client.CreateTopic(ctx, topicID)
		if err != nil {
			return nil, err
		}
	}

	return topic, nil
}

// realtimeTopology is the full topic -> pull-subscription set the Elixir
// realtime service consumes. It is the single source of truth for provisioning:
// every topic the StreamingPublisher writes to gets exactly one subscription
// named "<topic>-sub", matching :realtime, :pubsub_subscriptions in the Elixir
// config (realtime/config/{config,runtime}.exs). Keep the two lists in lockstep.
var realtimeTopology = map[string]string{
	TopicTaskStatus:     TopicTaskStatus + "-sub",
	TopicCampaignUpdate: TopicCampaignUpdate + "-sub",
	TopicWarmupUpdate:   TopicWarmupUpdate + "-sub",
	TopicEmailError:     TopicEmailError + "-sub",
	TopicEmailWarning:   TopicEmailWarning + "-sub",
	TopicUserEvents:     TopicUserEvents + "-sub",
	TopicEmailInbox:     TopicEmailInbox + "-sub",
	TopicBulkOps:        TopicBulkOps + "-sub",
	TopicContactsSync:   TopicContactsSync + "-sub",
}

// EnsureRealtimeTopology idempotently creates every realtime topic and its pull
// subscription. Safe to call on every boot and from multiple services
// concurrently: AlreadyExists is treated as success. Provisioning here (the
// control plane) means the Elixir Broadway producers always find their
// subscriptions, instead of silently consuming from a subscription that was
// never created. Called only when PUBSUB_ENABLED=true.
func (c *Client) EnsureRealtimeTopology(ctx context.Context) error {
	for topicID, subID := range realtimeTopology {
		topic, err := c.client.CreateTopic(ctx, topicID)
		if err != nil {
			if status.Code(err) != codes.AlreadyExists {
				return fmt.Errorf("create topic %s: %w", topicID, err)
			}
			topic = c.client.Topic(topicID)
		}
		if _, err := c.client.CreateSubscription(ctx, subID, pubsub.SubscriptionConfig{
			Topic:       topic,
			AckDeadline: 30 * time.Second,
		}); err != nil && status.Code(err) != codes.AlreadyExists {
			return fmt.Errorf("create subscription %s: %w", subID, err)
		}
	}
	return nil
}

// Publish publishes a message to a topic
func (c *Client) Publish(ctx context.Context, topicID string, data interface{}, attributes map[string]string) error {
	topic, err := c.getTopic(ctx, topicID)
	if err != nil {
		return fmt.Errorf("failed to get topic: %w", err)
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	result := topic.Publish(ctx, &pubsub.Message{
		Data:       jsonData,
		Attributes: attributes,
	})

	_, err = result.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}
