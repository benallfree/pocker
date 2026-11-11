// The speed page: measured results against npm PocketPages 0.x, and a live speed check that loads this server's
// own pages from the browser. The instruments are plain SVG/canvas driven by assets/speed.js.
const bench = require('../_private/bench.json')

type Run = { rps: number; p50: number; p99: number; single: number }
type BenchPage = { name: string; path: string; what: string; npm: Run; v1: Run }

function fmt(n: number): string {
  return n >= 1000 ? n.toLocaleString('en-US') : String(n)
}

function Duel({ page }: { page: BenchPage }) {
  const x = Math.round((page.v1.rps / page.npm.rps) * 10) / 10
  const rows: [string, keyof Run, string, boolean][] = [
    ['Requests/s', 'rps', '', true],
    ['p99 latency', 'p99', ' ms', false],
    ['One request', 'single', ' ms', false],
  ]
  return (
    <div class="card duel">
      <div class="duel-head">
        <div>
          <h3>
            {page.name} <span class="badge">{page.path}</span>
          </h3>
          <div class="muted">{page.what}</div>
        </div>
        <div class="mult" data-count={x} data-suffix="×">
          0×
        </div>
      </div>
      {rows.map(([label, key, unit, higherBetter]) => {
        const a = page.npm[key]
        const b = page.v1[key]
        const max = Math.max(a, b)
        return (
          <div class="metric">
            <div class="metric-label">
              {label} <span class="muted">{higherBetter ? 'higher is better' : 'lower is better'}</span>
            </div>
            <div class="bar-row">
              <span class="who">npm 0.22.5</span>
              <div class="track">
                <div class="fill old" data-w={(a / max) * 100}></div>
              </div>
              <span class="val" data-count={a} data-suffix={unit}>
                0
              </span>
            </div>
            <div class="bar-row">
              <span class="who">ext/pages</span>
              <div class="track">
                <div class="fill new" data-w={(b / max) * 100}></div>
              </div>
              <span class="val" data-count={b} data-suffix={unit}>
                0
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}

export default function Speed({ slot, asset }: any) {
  slot('title', 'Speed')
  slot('head', <link rel="stylesheet" href={asset('../assets/speed.css')} />)
  const targets: [string, string, string][] = [
    ['/', 'Home', 'EJS + partials'],
    ['/jsx', 'JSX page', 'TSX components'],
    ['/docs', 'Markdown', 'EJS → goldmark'],
    ['/notes', 'Notes', 'ctx.pb() query'],
    ['/stats', 'JSON', '+api.ts'],
    [asset('../assets/app.css'), 'Static file', 'fingerprinted CSS'],
    ['/api/health', 'PocketBase API', 'the ceiling'],
  ]
  return (
    <>
      <h1>Speed</h1>
      <p>
        Measured against npm PocketPages 0.22.5 on the same starter kit pages, then a live check you can run against this
        server right now.
      </p>

      <h2>ext/pages vs npm PocketPages 0.x</h2>
      <div class="duels">
        {bench.pages.map((p: BenchPage) => (
          <Duel page={p} />
        ))}
      </div>
      <p class="muted small">
        {bench.date}: {bench.machine}; 50 concurrent for 10 s, then one at a time. Details and the script are in the{' '}
        <code>ext/pages</code> README.
      </p>

      <h2>Speed check against itself</h2>
      <p>
        Your browser loads one of this demo's own pages as fast as it can. Every request is a real render: routing,
        middleware, layouts and templates. Browsers open about 6 connections per site, so this tops out lower than a
        load tester.
      </p>

      <div class="card rig" id="rig">
        <div class="controls">
          <div class="chips" role="radiogroup" aria-label="Page to test">
            {targets.map(([href, label, note], i) => (
              <button type="button" class={i === 0 ? 'chip on' : 'chip'} data-url={href} data-label={label}>
                <b>{label}</b>
                <span>{note}</span>
              </button>
            ))}
          </div>
          <div class="knobs">
            <label>
              Connections{' '}
              <select id="conc">
                <option>1</option>
                <option>2</option>
                <option>4</option>
                <option selected>6</option>
                <option>12</option>
              </select>
            </label>
            <label>
              Seconds{' '}
              <select id="secs">
                <option>3</option>
                <option selected>5</option>
                <option>10</option>
              </select>
            </label>
            <button type="button" id="go">
              Start
            </button>
            <button type="button" id="race" class="ghost">
              Race every page
            </button>
          </div>
        </div>

        <div class="dash">
          <div class="gauge-wrap">
            <svg viewBox="0 0 220 150" class="gauge" id="g-rps" aria-label="Requests per second"></svg>
            <div class="readout">
              <span id="rps">0</span>
              <small>requests/s</small>
            </div>
          </div>
          <div class="gauge-wrap">
            <svg viewBox="0 0 220 150" class="gauge" id="g-lat" aria-label="Median latency"></svg>
            <div class="readout">
              <span id="p50">0.0</span>
              <small>ms median</small>
            </div>
          </div>
          <div class="stats">
            <div>
              <span class="k">Requests</span>
              <span class="odo" id="total">0</span>
            </div>
            <div>
              <span class="k">p99</span>
              <span class="num" id="p99">–</span>
            </div>
            <div>
              <span class="k">Peak</span>
              <span class="num" id="peak">–</span>
            </div>
            <div>
              <span class="k">Errors</span>
              <span class="num" id="errs">0</span>
            </div>
          </div>
        </div>

        <div class="charts">
          <div>
            <div class="k">Requests/s over time</div>
            <canvas id="spark" height="90"></canvas>
          </div>
          <div>
            <div class="k">Latency histogram</div>
            <div class="hist" id="hist"></div>
          </div>
        </div>

        <div class="board" id="board" hidden>
          <div class="k">Leaderboard</div>
          <div id="board-rows"></div>
        </div>
      </div>

      <script src={asset('../assets/speed.js')}></script>
    </>
  )
}
