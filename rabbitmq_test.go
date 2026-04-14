package mqbridge_test

import (
	"testing"

	"github.com/fujiwara/mqbridge"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestMessageFromDelivery_MessageID(t *testing.T) {
	t.Run("preserves existing message_id", func(t *testing.T) {
		d := amqp.Delivery{
			MessageId:  "existing-id-123",
			Exchange:   "test-exchange",
			RoutingKey: "test-key",
		}
		msg := mqbridge.MessageFromDeliveryForTest(d)
		got := msg.Headers[mqbridge.HeaderRabbitMQMessageID]
		if got != "existing-id-123" {
			t.Errorf("expected message_id %q, got %q", "existing-id-123", got)
		}
	})

	t.Run("generates UUID when message_id is empty", func(t *testing.T) {
		d := amqp.Delivery{
			Exchange:   "test-exchange",
			RoutingKey: "test-key",
		}
		msg := mqbridge.MessageFromDeliveryForTest(d)
		got := msg.Headers[mqbridge.HeaderRabbitMQMessageID]
		if got == "" {
			t.Fatal("expected non-empty message_id, got empty")
		}
		if _, err := uuid.Parse(got); err != nil {
			t.Errorf("expected valid UUID, got %q: %v", got, err)
		}
	})

	t.Run("generates unique UUIDs for different messages", func(t *testing.T) {
		d := amqp.Delivery{
			Exchange:   "test-exchange",
			RoutingKey: "test-key",
		}
		msg1 := mqbridge.MessageFromDeliveryForTest(d)
		msg2 := mqbridge.MessageFromDeliveryForTest(d)
		id1 := msg1.Headers[mqbridge.HeaderRabbitMQMessageID]
		id2 := msg2.Headers[mqbridge.HeaderRabbitMQMessageID]
		if id1 == id2 {
			t.Errorf("expected unique UUIDs, got same value %q", id1)
		}
	})
}
