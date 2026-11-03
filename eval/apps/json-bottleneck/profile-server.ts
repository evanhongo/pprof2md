import express from 'express'
import { readFileSync, writeFileSync } from 'fs'

// Start profiler
const pprof = await import('@datadog/pprof')
pprof.time.start()

const app = express()

app.get('/config/:key', async (req, res) => {
  const { key } = req.params as { key: string }
  const config = JSON.parse(readFileSync('./config.json', 'utf-8'))
  res.json({ value: config[key] ?? null })
})

app.listen(3001, () => {
  console.log('Profiled server running on http://localhost:3001')
  console.log('Profile will be collected until SIGINT/SIGTERM...')
})

async function onSignal() {
  const profile = pprof.time.stop()
  const buf = await pprof.encode(profile);
  writeFileSync('./profile.pb.gz', buf)
  console.log('Profile saved to profile.pb.gz')
  process.exit(0)
}

process.on('SIGTERM', onSignal)
process.on('SIGINT', onSignal)

