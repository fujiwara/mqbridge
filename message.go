package mqbridge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Message is the common message type passed between Subscriber and Publisher.
type Message struct {
	// id is an in-memory identifier assigned by the subscriber (e.g. SimpleMQ message ID).
	// It is unexported to clarify that it is not included in the wire format.
	id      string
	Body    []byte
	Headers map[string]string
}

// MessageError represents an error caused by the message content itself
// (e.g. missing required headers). Messages that cause this error cannot
// be processed regardless of retries and should be dropped.
type MessageError struct {
	Err error
}

func (e *MessageError) Error() string { return e.Err.Error() }
func (e *MessageError) Unwrap() error { return e.Err }

// bodyEncodingBase64 is the wire format value for base64-encoded body.
const bodyEncodingBase64 = "base64"

// Header key constants for RabbitMQ-specific metadata.
const (
	HeaderRabbitMQExchange      = "rabbitmq.exchange"
	HeaderRabbitMQRoutingKey    = "rabbitmq.routing_key"
	HeaderRabbitMQReplyTo       = "rabbitmq.reply_to"
	HeaderRabbitMQCorrelationID = "rabbitmq.correlation_id"
	HeaderRabbitMQContentType   = "rabbitmq.content_type"
	HeaderRabbitMQMessageID     = "rabbitmq.message_id"
	// HeaderRabbitMQHeaderPrefix is used for custom AMQP headers.
	// e.g. an AMQP header "x-foo" becomes "rabbitmq.header.x-foo".
	HeaderRabbitMQHeaderPrefix = "rabbitmq.header."
)

// messageWire is the JSON serialization format for Message on SimpleMQ.
// Body is a *string to distinguish between absent (legacy format) and empty body.
type messageWire struct {
	Headers      map[string]string `json:"headers,omitempty"`
	Body         *string           `json:"body"`
	BodyEncoding string            `json:"body_encoding,omitempty"` // "base64" for binary-safe
}

// MarshalMessage serializes a Message to JSON (for SimpleMQ transport).
// If the body is valid UTF-8, it is stored as a plain string.
// Otherwise, it is base64-encoded and body_encoding is set to "base64".
func MarshalMessage(msg *Message) ([]byte, error) {
	w := messageWire{
		Headers: msg.Headers,
	}
	if utf8.Valid(msg.Body) {
		s := string(msg.Body)
		w.Body = &s
	} else {
		s := base64.StdEncoding.EncodeToString(msg.Body)
		w.Body = &s
		w.BodyEncoding = bodyEncodingBase64
	}
	return json.Marshal(w)
}

// UnmarshalMessage deserializes a Message from JSON.
// If body_encoding is "base64", the body is decoded from base64.
// If the data is not valid messageWire JSON or the body field is absent,
// it falls back to treating the entire data as the message body (backward compatibility).
func UnmarshalMessage(data []byte) *Message {
	var w messageWire
	if err := json.Unmarshal(data, &w); err == nil && w.Body != nil {
		var body []byte
		if w.BodyEncoding == bodyEncodingBase64 {
			decoded, err := base64.StdEncoding.DecodeString(*w.Body)
			if err != nil {
				// Invalid base64; treat as raw string.
				body = []byte(*w.Body)
			} else {
				body = decoded
			}
		} else {
			body = []byte(*w.Body)
		}
		return &Message{
			Body:    body,
			Headers: w.Headers,
		}
	}
	// Fallback: treat entire data as body (legacy format).
	return &Message{
		Body: data,
	}
}

// ValidateForRabbitMQ checks that the message has the required headers
// for publishing to RabbitMQ (rabbitmq.exchange and rabbitmq.routing_key).
// Both keys must be present but may be empty strings.
func (m *Message) ValidateForRabbitMQ() error {
	if _, ok := m.Headers[HeaderRabbitMQExchange]; !ok {
		return fmt.Errorf("header %q is required", HeaderRabbitMQExchange)
	}
	if _, ok := m.Headers[HeaderRabbitMQRoutingKey]; !ok {
		return fmt.Errorf("header %q is required", HeaderRabbitMQRoutingKey)
	}
	return nil
}

// RabbitMQPublishParams extracts RabbitMQ publishing parameters from a Message.
// Returns exchange, routingKey, and custom AMQP headers.
// Exchange may be empty (AMQP default exchange). RoutingKey may also be empty
// (e.g. for fanout exchanges). Both header keys must be present.
func (m *Message) RabbitMQPublishParams() (exchange, routingKey string, headers map[string]string, err error) {
	if err := m.ValidateForRabbitMQ(); err != nil {
		return "", "", nil, err
	}
	exchange = m.Headers[HeaderRabbitMQExchange]
	routingKey = m.Headers[HeaderRabbitMQRoutingKey]
	headers = make(map[string]string)
	for k, v := range m.Headers {
		if len(k) > len(HeaderRabbitMQHeaderPrefix) && k[:len(HeaderRabbitMQHeaderPrefix)] == HeaderRabbitMQHeaderPrefix {
			headers[k[len(HeaderRabbitMQHeaderPrefix):]] = v
		}
	}
	return exchange, routingKey, headers, nil
}
