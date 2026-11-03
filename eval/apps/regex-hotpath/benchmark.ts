import autocannon from 'autocannon'

// 20 emails per request, one of them invalid
const emails = [
  'test1@example.com', 'test2@example.com', 'test3@example.com', 'test4@example.com',
  'test5@example.com', 'test6@example.com', 'test7@example.com', 'test8@example.com',
  'test9@example.com', 'test10@example.com', 'invalid', 'another@test.org',
  'user@domain.co', 'name@company.io', 'hello@world.net', 'foo@bar.com',
  'a@b.co', 'test@test.test', 'email@email.email', 'x@y.zz'
]

const body = JSON.stringify({ emails })

const result = await autocannon({
  url: 'http://localhost:3002/validate',
  duration: 10,
  connections: 10,
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body
})

console.log(autocannon.printResult(result))
