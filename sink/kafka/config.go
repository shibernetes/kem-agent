package kafka

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/sink"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "kafka"
)

const (
	defaultClientID    = version.Name
	defaultSendTimeout = units.Duration(5 * time.Second)

	// defaultMaxMessageBytes is the record size limit that franz-go and
	// the brokers apply by default.
	defaultMaxMessageBytes = units.Bytes(1000012)
)

const (
	// maxTopicLength is the longest topic name that Kafka accepts.
	maxTopicLength = 249

	// minMessageBytes and maxMessageBytes bound the record size limit to
	// the range that franz-go accepts.
	minMessageBytes = units.Bytes(512)
	maxMessageBytes = units.Bytes(1 << 30) // 1 GiB

	// minSendTimeout keeps the delivery timeout, which is half of the send
	// timeout, at or above the second that franz-go requires.
	minSendTimeout = units.Duration(2 * time.Second)
)

var _ sink.Drainable = Config{}

// Config defines the configuration of the Kafka sink.
type Config struct {
	// Sink type
	Type string `yaml:"type"`

	// List of broker addresses the client first connects to, as host:port
	Brokers []string `yaml:"brokers"`

	// Topic name the records are produced to
	Topic string `yaml:"topic"`

	// Event field written as the key of each record. Records that share a
	// key keep their order
	MessageKey MessageKey `yaml:"message_key,omitempty"`

	// Replicas that must store a record before it counts as delivered. The
	// numbers 1 and 0 are also accepted, for leader and none
	RequiredAcks RequiredAcks `yaml:"required_acks,omitempty"`

	// Compression algorithm name
	Compression Compression `yaml:"compression,omitempty"`

	// Client ID sent to the brokers
	ClientID string `yaml:"client_id,omitempty"`

	// Maximum size of a record. A larger record is refused without being sent
	MaxMessageBytes units.Bytes `yaml:"max_message_bytes,omitempty"`

	// Allow the brokers to create the topic if it doesn't exist
	AllowAutoTopicCreation bool `yaml:"allow_auto_topic_creation,omitempty"`

	// Maximum time of a single delivery attempt
	SendTimeout units.Duration `yaml:"send_timeout,omitempty"`

	// Configuration of the authentication
	Auth AuthenticationConfig `yaml:"auth,omitempty"`

	// Configuration of the TLS connection
	TLS tlsconfig.Config `yaml:"tls,omitempty"`

	// Configuration of the batching strategy
	Batch sink.BatchConfig `yaml:"batch,omitempty"`

	// Configuration of the events queue
	Queue sink.QueueConfig `yaml:"queue,omitempty"`

	// Configuration of the retry strategy
	Retry sink.RetryConfig `yaml:"retry,omitempty"`
}

// AuthenticationConfig defines how the client authenticates to the brokers.
// An empty configuration connects without authentication.
type AuthenticationConfig struct {
	// Configuration of the SASL authentication
	SASL *SASLConfig `yaml:"sasl,omitempty"`
}

// SASLConfig defines the SASL credentials of the client.
type SASLConfig struct {
	// SASL mechanism the client authenticates with
	Mechanism SASLMechanism `yaml:"mechanism"`

	// Name of the user
	Username string `yaml:"username"`

	// Password of the user
	Password opaque.String `yaml:"password"`
}

// DefaultConfig returns the default configuration.
// The connection uses TLS, and a record counts as delivered once every
// in-sync replica stores it.
func DefaultConfig() Config {
	return Config{
		Type:            TypeName,
		MessageKey:      MessageKeyUID,
		RequiredAcks:    RequiredAcksAll,
		Compression:     CompressionSnappy,
		ClientID:        defaultClientID,
		MaxMessageBytes: defaultMaxMessageBytes,
		SendTimeout:     defaultSendTimeout,
		Batch:           sink.DefaultBatchConfig(),
		Queue:           sink.DefaultQueueConfig(),
		Retry:           sink.DefaultRetryConfig(),
	}
}

// Validate validates the configuration.
// A SASL password is only accepted over TLS.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if err := c.validate(); err != nil {
		return err
	}
	return sink.ValidateSharedConfigs(c.Batch, c.Queue, c.Retry)
}

// DrainerConfig implements the [sink.Drainable] interface.
func (c Config) DrainerConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch:       c.Batch,
		Queue:       c.Queue,
		Retry:       c.Retry,
		SendTimeout: c.SendTimeout,
	}
}

// validate checks the settings that belong to the sink itself.
func (c Config) validate() error {
	if len(c.Brokers) == 0 {
		// The required check only reports a list that is missing,
		// so an empty one is caught here.
		return diag.Pathf("brokers", "brokers must contain at least one address")
	}
	for i, addr := range c.Brokers {
		if err := validateBroker(addr); err != nil {
			return diag.Path(fmt.Sprintf("brokers[%d]", i), err)
		}
	}
	if err := validateTopic(c.Topic); err != nil {
		return diag.Path("topic", err)
	}
	if err := c.MessageKey.Validate(); err != nil {
		return diag.Prefix(err, "message_key")
	}
	if err := c.RequiredAcks.Validate(); err != nil {
		return diag.Prefix(err, "required_acks")
	}
	if err := c.Compression.Validate(); err != nil {
		return diag.Prefix(err, "compression")
	}
	if c.MaxMessageBytes < minMessageBytes || c.MaxMessageBytes > maxMessageBytes {
		return diag.Pathf("max_message_bytes", "max_message_bytes must be between %s and %s", minMessageBytes, maxMessageBytes)
	}
	if c.SendTimeout < minSendTimeout {
		return diag.Pathf("send_timeout", "send_timeout must be at least %s", minSendTimeout)
	}
	if err := c.validateSASL(); err != nil {
		return err
	}
	if err := c.TLS.Validate(); err != nil {
		return diag.Prefix(err, "tls")
	}
	return nil
}

// validateSASL checks the SASL settings.
func (c Config) validateSASL() error {
	sasl := c.Auth.SASL
	if sasl == nil {
		return nil
	}
	if err := sasl.Mechanism.Validate(); err != nil {
		return diag.Prefix(err, "auth.sasl.mechanism")
	}
	if c.TLS.Insecure && sasl.Password != "" {
		return diag.Pathf("auth.sasl.password", "auth: sasl password requires TLS, which tls.insecure turns off")
	}
	return nil
}

// validateBroker checks that a broker address is made of a host and a port.
func validateBroker(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || strings.ContainsAny(host, "/ ") {
		return fmt.Errorf("invalid broker address %q, must be host:port", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port in broker address %q", addr)
	}
	return nil
}

// validateTopic checks a topic name against the rules that Kafka applies.
func validateTopic(topic string) error {
	switch {
	case topic == "." || topic == "..":
		return fmt.Errorf("topic %q is not allowed", topic)
	case len(topic) > maxTopicLength:
		return fmt.Errorf("topic %q is longer than %d characters", topic, maxTopicLength)
	case strings.ContainsFunc(topic, invalidTopicRune):
		return fmt.Errorf("invalid topic %q, allowed characters are letters, digits, dots, underscores and dashes", topic)
	}
	return nil
}

// invalidTopicRune reports whether r is outside the characters that
// Kafka allows in a topic name.
func invalidTopicRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		return false
	}
	return true
}
