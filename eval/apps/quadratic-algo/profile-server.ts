import express from 'express'
import { writeFileSync } from 'fs'

// Start profiler
const pprof = await import('@datadog/pprof')
pprof.time.start()

const app = express()
app.use(express.json({ limit: '10mb' }))

interface Item {
  id: number
  name: string
  value: number
}

app.post('/dedupe', async (req, res) => {
  const items: Item[] = req.body as Item[]
  const unique: Item[] = []

  for (const item of items) {
    let exists = false
    for (const u of unique) {
      if (u.id === item.id) {
        exists = true
        break
      }
    }
    if (!exists) {
      unique.push(item)
    }
  }

  res.json(unique)
})

app.listen(3005, () => {
  console.log('Profiled server running on http://localhost:3005')
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
