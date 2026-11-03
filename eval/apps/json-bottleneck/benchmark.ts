import autocannon from 'autocannon'

// Pick a random key from config.json (key0 ~ key9999) on every request
const KEY_COUNT = 10000

const result = await autocannon({
  url: 'http://localhost:3001',
  duration: 10,
  connections: 10,
  requests: [{
    setupRequest: (req) => ({
      ...req,
      path: `/config/key${Math.floor(Math.random() * KEY_COUNT)}`
    })
  }]
})

console.log(autocannon.printResult(result))
