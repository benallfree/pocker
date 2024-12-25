import http from 'k6/http'
import { group } from 'k6'
import { Trend } from 'k6/metrics'

const subdomain = `continent-powerful`
const directUrl = `https://${subdomain}.pockethost.io/api/health`
const pockerUrl = `https://${subdomain}.pocker.pockethost.io/api/health`
const pockerCfUrl = `https://${subdomain}.pockercf.pockethost.io/api/health`

const directDuration = new Trend('http_req_duration_direct')
const pockerDuration = new Trend('http_req_duration_pocker')
const pockerCfDuration = new Trend('http_req_duration_pocker_cf')
const pockerDurationInternal = new Trend('http_req_duration_pocker_internal')
const pockerCfDurationInternal = new Trend(
  'http_req_duration_pocker_cf_internal'
)

export function setup() {
  http.get(directUrl)
  const pockerResponse = http.get(pockerUrl)
  console.log('Pocker Region:', pockerResponse.headers[`X-Pockethost-Region`])
}

export default function () {
  group('Direct', () => {
    const directResponse = http.get(directUrl)
    directDuration.add(directResponse.timings.duration)
    if (directResponse.status !== 200) {
      console.error(
        `Direct request failed with status ${directResponse.status}`
      )
      throw new Error(`Expected status 200, got ${directResponse.status}`)
    }
  })

  group('Pocker', () => {
    const pockerResponse = http.get(pockerUrl)
    pockerDuration.add(pockerResponse.timings.duration)
    pockerDurationInternal.add(
      parseInt(pockerResponse.headers[`X-Pockethost-Request-Duration`], 10)
    )
    if (pockerResponse.status !== 200) {
      console.error(
        `Pocker request failed with status ${pockerResponse.status}`
      )
      throw new Error(`Expected status 200, got ${pockerResponse.status}`)
    }
  })

  group('Pocker CF', () => {
    const pockerCfResponse = http.get(pockerCfUrl)
    pockerCfDuration.add(pockerCfResponse.timings.duration)
    pockerCfDurationInternal.add(
      parseInt(pockerCfResponse.headers[`X-Pockethost-Request-Duration`], 10)
    )
    if (pockerCfResponse.status !== 200) {
      console.error(
        `Pocker CF request failed with status ${pockerCfResponse.status}`
      )
      throw new Error(`Expected status 200, got ${pockerCfResponse.status}`)
    }
  })
}

// Disable the default summary
export const handleSummary = (data) => {
  // console.log(JSON.stringify(data.metrics, null, 2))
  const p95Direct = data.metrics.http_req_duration_direct.values['p(95)']
  const p95Pocker = data.metrics.http_req_duration_pocker.values['p(95)']
  const p95PockerCf = data.metrics.http_req_duration_pocker_cf.values['p(95)']
  const p95PockerInternal =
    data.metrics.http_req_duration_pocker_internal.values['p(95)']
  const p95PockerCfInternal =
    data.metrics.http_req_duration_pocker_cf_internal.values['p(95)']

  console.log('P(95) Metrics:')
  console.log(`- CF->DO(sfo): ${p95Direct.toFixed(2)}ms`)
  console.log(`- Fly(edge)->Fly(sjc)->DO(sfo): ${p95Pocker.toFixed(2)}ms`)
  console.log(`- CF->Fly(edge)->Fly(sjc)->DO(sfo): ${p95PockerCf.toFixed(2)}ms`)
  return {} // Return empty object to disable default summary
}

export const options = {
  vus: 10,
  iterations: 100,
}
