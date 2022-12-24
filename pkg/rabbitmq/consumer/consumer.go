package consumer

import (
	"context"

	"github.com/pkg/errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/exp/slog"
)

const (
	_exchangeKind       = "direct"
	_exchangeDurable    = true
	_exchangeAutoDelete = false
	_exchangeInternal   = false
	_exchangeNoWait     = false

	_queueDurable    = true
	_queueAutoDelete = false
	_queueExclusive  = false
	_queueNoWait     = false

	_prefetchCount  = 1
	_prefetchSize   = 0
	_prefetchGlobal = false

	_consumeAutoAck   = false
	_consumeExclusive = false
	_consumeNoLocal   = false
	_consumeNoWait    = false

	_exchangeName   = "orders-exchange"
	_queueName      = "orders-queue"
	_bindingKey     = "orders-routing-key"
	_consumerTag    = "orders-consumer"
	_workerPoolSize = 24
)

type worker func(ctx context.Context, messages <-chan amqp.Delivery)

type Consumer struct {
	exchangeName, queueName, bindingKey, consumerTag string
	workerPoolSize                                   int
	amqpConn                                         *amqp.Connection
}

func NewConsumer(
	amqpConn *amqp.Connection,
	opts ...Option,
) (*Consumer, error) {
	sub := &Consumer{
		amqpConn:       amqpConn,
		exchangeName:   _exchangeName,
		queueName:      _queueName,
		bindingKey:     _bindingKey,
		consumerTag:    _consumerTag,
		workerPoolSize: _workerPoolSize,
	}

	for _, opt := range opts {
		opt(sub)
	}

	return sub, nil
}

// CreateChannel Consume messages.
func (c *Consumer) CreateChannel() (*amqp.Channel, error) {
	ch, err := c.amqpConn.Channel()
	if err != nil {
		return nil, errors.Wrap(err, "Error amqpConn.Channel")
	}

	slog.Info("declaring exchange", "exchange_name", c.exchangeName)
	err = ch.ExchangeDeclare(
		c.exchangeName,
		_exchangeKind,
		_exchangeDurable,
		_exchangeAutoDelete,
		_exchangeInternal,
		_exchangeNoWait,
		nil,
	)

	if err != nil {
		return nil, errors.Wrap(err, "Error ch.ExchangeDeclare")
	}

	queue, err := ch.QueueDeclare(
		c.queueName,
		_queueDurable,
		_queueAutoDelete,
		_queueExclusive,
		_queueNoWait,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "Error ch.QueueDeclare")
	}

	slog.Info("declared queue, binding it to exchange", "queue", queue.Name, "messages_count", queue.Messages,
		"consumer_count", queue.Consumers, "exchange", c.exchangeName, "binding_key", c.bindingKey,
	)

	err = ch.QueueBind(
		queue.Name,
		c.bindingKey,
		c.exchangeName,
		_queueNoWait,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "Error ch.QueueBind")
	}

	slog.Info("queue bound to exchange, starting to consume from queue", "consumer_tag", c.consumerTag)

	err = ch.Qos(
		_prefetchCount,  // prefetch count
		_prefetchSize,   // prefetch size
		_prefetchGlobal, // global
	)
	if err != nil {
		return nil, errors.Wrap(err, "Error ch.Qos")
	}

	return ch, nil
}

// StartConsumer Start new rabbitmq consumer.
func (c *Consumer) StartConsumer(fn worker) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := c.CreateChannel()
	if err != nil {
		return errors.Wrap(err, "CreateChannel")
	}
	defer ch.Close()

	deliveries, err := ch.Consume(
		c.queueName,
		c.consumerTag,
		_consumeAutoAck,
		_consumeExclusive,
		_consumeNoLocal,
		_consumeNoWait,
		nil,
	)
	if err != nil {
		return errors.Wrap(err, "Consume")
	}

	forever := make(chan bool)

	for i := 0; i < c.workerPoolSize; i++ {
		go fn(ctx, deliveries)
	}

	chanErr := <-ch.NotifyClose(make(chan *amqp.Error))
	slog.Error("ch.NotifyClose", chanErr)
	<-forever

	return chanErr
}
