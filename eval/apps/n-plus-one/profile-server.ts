import express from 'express'
import { writeFileSync } from 'fs'

// Start profiler
const pprof = await import('@datadog/pprof')
pprof.time.start()

const app = express()

async function fetchUser(id: number): Promise<{ id: number; name: string }> {
  await new Promise(resolve => setTimeout(resolve, 100))
  return { id, name: `User ${id}` }
}

async function fetchUserPosts(userId: number): Promise<Array<{ id: number; title: string }>> {
  await new Promise(resolve => setTimeout(resolve, 100))
  return [
    { id: userId * 100 + 1, title: `Post 1 by user ${userId}` },
    { id: userId * 100 + 2, title: `Post 2 by user ${userId}` }
  ]
}

app.get('/users-with-posts', async (_req, res) => {
  const userIds = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
  const results: Array<{ user: { id: number; name: string }; posts: Array<{ id: number; title: string }> }> = []

  for (const id of userIds) {
    const user = await fetchUser(id)
    const posts = await fetchUserPosts(id)
    results.push({ user, posts })
  }

  res.json(results)
})

app.listen(3003, () => {
  console.log('Profiled server running on http://localhost:3003')
  console.log('Profile will be collected until SIGINT/SIGTERM...')
})

async function onSignal() {
  const profile = pprof.time.stop()
  const buf = await pprof.encode(profile)
  writeFileSync('./profile.pb.gz', buf)
  console.log('Profile saved to profile.pb.gz')
  process.exit(0)
}

process.on('SIGTERM', onSignal)
process.on('SIGINT', onSignal)
