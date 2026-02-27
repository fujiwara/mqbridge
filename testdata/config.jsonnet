{
  rabbitmq: {
    url: 'amqp://guest:guest@localhost:5672/',
  },
  simplemq: {
    api_url: 'http://localhost:18080',
  },
  bridges: [
    {
      // RabbitMQ → SimpleMQ
      from: {
        rabbitmq: {
          queue: 'test-source-queue',
          exchange: 'test-exchange',
          exchange_type: 'topic',
          routing_key: '#',
        },
      },
      to: [
        { simplemq: { queue: 'test-dest-queue', api_key: 'test-api-key' } },
      ],
    },
    {
      // SimpleMQ → RabbitMQ
      from: {
        simplemq: {
          queue: 'test-inbound-queue',
          api_key: 'test-api-key',
          polling_interval: '100ms',
        },
      },
      to: [
        { rabbitmq: {} },
      ],
    },
  ],
}
