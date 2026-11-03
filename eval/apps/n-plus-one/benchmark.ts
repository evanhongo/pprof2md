import autocannon from 'autocannon'

const result = await autocannon({
  url: 'http://localhost:3003/users-with-posts',
  duration: 10,
  connections: 10
})

console.log(autocannon.printResult(result))
