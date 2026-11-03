import express from 'express'
import { writeFileSync } from 'fs'

// Start profiler
const pprof = await import('@datadog/pprof')
pprof.time.start()

const app = express()
app.use(express.json({ limit: '10mb' }))

app.post('/validate', async (req, res) => {
  const { emails } = req.body as { emails: string[] }
  const results: Array<{ email: string; valid: boolean }> = []

  for (const email of emails) {
    const emailPattern = new RegExp('^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$')
    results.push({ email, valid: emailPattern.test(email) })
  }

  res.json(results)
})

app.listen(3002, () => {
  console.log('Profiled server running on http://localhost:3002')
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
