import express from 'express'
import { writeFileSync } from 'fs'

// Start heap profiler: sample every 512KB, max stack depth 64.
// allocations=true also records alloc_objects/alloc_space (requires Node.js >= 26),
// so short-lived garbage shows up, not just objects still live at shutdown.
const pprof = await import('@datadog/pprof')
pprof.heap.start(512 * 1024, 64, true)

const app = express()
app.use(express.json({ limit: '10mb' }))

interface Item {
  id: number
  value: number
  category: string
}

app.post('/transform', async (req, res) => {
  let data: Item[] = req.body as Item[]

  data = data.map(x => ({ ...x, processed: true }))
  data = data.filter(x => x.value > 10)
  data = data.map(x => ({ ...x, doubled: x.value * 2 }))
  data = data.filter(x => x.value < 900)

  res.json(data)
})

app.listen(3004, () => {
  console.log('Profiled server running on http://localhost:3004')
  console.log('Heap profile will be collected until SIGINT/SIGTERM...')
})

async function onSignal() {
  const profile = pprof.heap.profile()
  const buf = await pprof.encode(profile)
  writeFileSync('./profile.pb.gz', buf)
  console.log('Profile saved to profile.pb.gz')
  process.exit(0)
}

process.on('SIGTERM', onSignal)
process.on('SIGINT', onSignal)
