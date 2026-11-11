// The speed page: count-up results, and a live speed check (concurrent fetch loops against this server) driving two
// SVG gauges, an odometer, a sparkline and a latency histogram. No libraries.
;(function () {
  'use strict'
  var $ = function (id) { return document.getElementById(id) }
  var reduce = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches

  // ---- head-to-head: count up and grow bars when they scroll into view ----
  function countUp(el) {
    var target = parseFloat(el.dataset.count), suffix = el.dataset.suffix || ''
    var decimals = target % 1 ? 1 : 0, t0 = performance.now(), dur = reduce ? 0 : 1400
    function step(now) {
      var k = dur ? Math.min(1, (now - t0) / dur) : 1
      var e = 1 - Math.pow(1 - k, 3)
      var v = target * e
      el.textContent = (decimals ? v.toFixed(1) : Math.round(v).toLocaleString('en-US')) + suffix
      if (k < 1) requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  }
  var duels = document.querySelector('.duels')
  if (duels) {
    var fire = function () {
      duels.querySelectorAll('[data-count]').forEach(countUp)
      duels.querySelectorAll('.fill').forEach(function (f) { f.style.width = f.dataset.w + '%' })
    }
    if ('IntersectionObserver' in window) {
      var io = new IntersectionObserver(function (es) {
        if (es.some(function (e) { return e.isIntersecting })) { io.disconnect(); fire() }
      }, { threshold: 0.3 })
      io.observe(duels)
    } else fire()
  }

  // ---- gauges ----
  var NS = 'http://www.w3.org/2000/svg', CX = 110, CY = 112, R = 86, A0 = -210, SWEEP = 240
  function el(name, attrs, parent) {
    var e = document.createElementNS(NS, name)
    for (var k in attrs) e.setAttribute(k, attrs[k])
    parent.appendChild(e)
    return e
  }
  function polar(deg, r) {
    var a = (deg * Math.PI) / 180
    return [CX + r * Math.cos(a), CY + r * Math.sin(a)]
  }
  function arcPath(r) {
    var s = polar(A0, r), e = polar(A0 + SWEEP, r)
    return 'M' + s[0] + ' ' + s[1] + ' A' + r + ' ' + r + ' 0 1 1 ' + e[0] + ' ' + e[1]
  }
  // Gauge: toPos maps a value to 0..1; ticks are [value, label] pairs.
  function Gauge(svg, opts) {
    this.svg = svg; this.opts = opts; this.pos = 0; this.target = 0
    svg.innerHTML = ''
    el('path', { d: arcPath(R), class: 'arc-bg' }, svg)
    var grad = el('linearGradient', { id: svg.id + '-grad', x1: '0', x2: '1' }, el('defs', {}, svg))
    el('stop', { offset: '0', 'stop-color': opts.from }, grad)
    el('stop', { offset: '1', 'stop-color': opts.to }, grad)
    this.arc = el('path', { d: arcPath(R), class: 'arc', stroke: 'url(#' + svg.id + '-grad)' }, svg)
    this.len = this.arc.getTotalLength()
    this.arc.style.strokeDasharray = this.len
    this.arc.style.strokeDashoffset = this.len
    this.tickG = el('g', {}, svg)
    this.needle = el('line', { x1: CX, y1: CY, x2: CX, y2: CY - R + 18, class: 'needle' }, svg)
    el('circle', { cx: CX, cy: CY, r: 6, class: 'hub' }, svg)
    this.setTicks(opts.ticks)
    this.render()
  }
  Gauge.prototype.setTicks = function (ticks) {
    var g = this.tickG, toPos = this.opts.toPos
    g.innerHTML = ''
    ticks.forEach(function (t) {
      var deg = A0 + SWEEP * toPos(t[0])
      var a = polar(deg, R - 10), b = polar(deg, R - 18), c = polar(deg, R - 28)
      el('line', { x1: a[0], y1: a[1], x2: b[0], y2: b[1], class: 'tick' }, g)
      var tx = el('text', { x: c[0], y: c[1] + 3, class: 'tick-label' }, g)
      tx.textContent = t[1]
    })
  }
  Gauge.prototype.set = function (v) { this.target = Math.max(0, Math.min(1, this.opts.toPos(v))) }
  Gauge.prototype.render = function () {
    this.pos += (this.target - this.pos) * (reduce ? 1 : 0.12)
    var deg = A0 + SWEEP * this.pos + 90
    this.needle.setAttribute('transform', 'rotate(' + deg + ' ' + CX + ' ' + CY + ')')
    this.arc.style.strokeDashoffset = this.len * (1 - this.pos)
  }

  var rig = $('rig')
  if (!rig) return

  var css = getComputedStyle(document.documentElement)
  var accent = css.getPropertyValue('--accent').trim() || '#0a66c2'
  var NICE = [100, 200, 500, 1000, 2000, 5000, 10000, 20000, 50000, 100000]
  function short(n) { return n >= 1000 ? n / 1000 + 'k' : String(n) }
  function rpsTicks(max) {
    var out = []
    for (var i = 0; i <= 4; i++) out.push([(max * i) / 4, short((max * i) / 4)])
    return out
  }
  var rpsMax = 1000
  var gRps = new Gauge($('g-rps'), {
    from: accent, to: '#34c759', ticks: rpsTicks(rpsMax),
    toPos: function (v) { return v / rpsMax },
  })
  // latency: log scale, 0.1 ms to 100 ms
  var gLat = new Gauge($('g-lat'), {
    from: '#34c759', to: '#ff7a45',
    ticks: [[0.1, '.1'], [1, '1'], [10, '10'], [100, '100']],
    toPos: function (v) { return v <= 0.1 ? 0 : Math.log10(v / 0.1) / 3 },
  })
  function frame() { gRps.render(); gLat.render(); requestAnimationFrame(frame) }
  requestAnimationFrame(frame)

  // histogram
  var EDGES = [0.5, 1, 2, 4, 8, 16, 32, 64, Infinity]
  var LABELS = ['<½', '1', '2', '4', '8', '16', '32', '64', '64+']
  var hist = $('hist'), bars = []
  LABELS.forEach(function (l, i) {
    var b = document.createElement('div')
    b.className = 'b' + (i >= 6 ? ' slow' : '')
    b.innerHTML = '<i></i><span>' + l + '</span>'
    hist.appendChild(b)
    bars.push(b.firstChild)
  })
  function bucket(ms) { for (var i = 0; i < EDGES.length; i++) if (ms < EDGES[i]) return i; return EDGES.length - 1 }

  // sparkline
  var spark = $('spark'), sctx = spark.getContext('2d')
  function drawSpark(series, max) {
    var dpr = window.devicePixelRatio || 1, w = spark.clientWidth, h = spark.clientHeight || 90
    spark.width = w * dpr; spark.height = h * dpr
    sctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    sctx.clearRect(0, 0, w, h)
    if (series.length < 2) return
    var step = w / Math.max(series.length - 1, 1)
    sctx.beginPath()
    series.forEach(function (v, i) {
      var x = i * step, y = h - 6 - (v / max) * (h - 14)
      i ? sctx.lineTo(x, y) : sctx.moveTo(x, y)
    })
    sctx.strokeStyle = accent; sctx.lineWidth = 2; sctx.stroke()
    sctx.lineTo((series.length - 1) * step, h); sctx.lineTo(0, h); sctx.closePath()
    var g = sctx.createLinearGradient(0, 0, 0, h)
    g.addColorStop(0, accent + '55'); g.addColorStop(1, accent + '00')
    sctx.fillStyle = g; sctx.fill()
  }

  function pct(sorted, p) {
    if (!sorted.length) return 0
    return sorted[Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length))]
  }
  function fmtMs(ms) { return ms < 10 ? ms.toFixed(2) : ms.toFixed(1) }

  // ---- one run: `conc` fetch loops for `secs` seconds ----
  function run(url, conc, secs) {
    var lat = [], counts = new Array(EDGES.length).fill(0), total = 0, errs = 0, peak = 0
    var done = [], series = [], end = performance.now() + secs * 1000
    gRps.set(0)
    async function loop() {
      while (performance.now() < end) {
        var t0 = performance.now()
        try {
          var r = await fetch(url, { cache: 'no-store', credentials: 'same-origin' })
          await r.arrayBuffer()
          if (!r.ok) errs++
        } catch (e) {
          errs++
        }
        var ms = performance.now() - t0, now = performance.now()
        lat.push(ms); done.push(now); total++
        counts[bucket(ms)]++
      }
    }
    var ticker = setInterval(function () {
      var now = performance.now()
      while (done.length && done[0] < now - 1000) done.shift()
      var rps = done.length
      peak = Math.max(peak, rps)
      series.push(rps)
      var want = NICE.find(function (n) { return n >= peak * 1.15 }) || NICE[NICE.length - 1]
      if (want !== rpsMax) { rpsMax = want; gRps.setTicks(rpsTicks(rpsMax)) }
      gRps.set(rps)
      var recent = lat.slice(-400).sort(function (a, b) { return a - b })
      var p50 = pct(recent, 50)
      gLat.set(p50)
      $('rps').textContent = rps.toLocaleString('en-US')
      $('p50').textContent = fmtMs(p50)
      $('p99').textContent = fmtMs(pct(recent, 99)) + ' ms'
      $('peak').textContent = peak.toLocaleString('en-US') + '/s'
      $('total').textContent = String(total).padStart(7, '0')
      $('errs').textContent = errs
      var maxc = Math.max.apply(null, counts) || 1
      counts.forEach(function (c, i) { bars[i].style.height = (c / maxc) * 100 + '%' })
      drawSpark(series, rpsMax)
    }, 250)
    var loops = []
    for (var i = 0; i < conc; i++) loops.push(loop())
    return Promise.all(loops).then(function () {
      clearInterval(ticker)
      var sorted = lat.slice().sort(function (a, b) { return a - b })
      var res = { rps: Math.round(total / secs), p50: pct(sorted, 50), p99: pct(sorted, 99), total: total, errs: errs, peak: peak }
      // settle on the run's averages
      gRps.set(res.rps); gLat.set(res.p50)
      $('rps').textContent = res.rps.toLocaleString('en-US')
      $('p50').textContent = fmtMs(res.p50)
      $('p99').textContent = fmtMs(res.p99) + ' ms'
      $('total').textContent = String(total).padStart(7, '0')
      return res
    })
  }

  // ---- controls ----
  var chips = Array.prototype.slice.call(rig.querySelectorAll('.chip'))
  chips.forEach(function (c) {
    c.addEventListener('click', function () {
      chips.forEach(function (x) { x.classList.toggle('on', x === c) })
    })
  })
  var go = $('go'), race = $('race'), busy = false
  function lock(on) { busy = on; go.disabled = race.disabled = on; chips.forEach(function (c) { c.disabled = on }) }
  function selected() { return chips.find(function (c) { return c.classList.contains('on') }) || chips[0] }
  function settings() { return { conc: +$('conc').value, secs: +$('secs').value } }

  go.addEventListener('click', function () {
    if (busy) return
    var s = settings(), chip = selected()
    lock(true); go.textContent = 'Running…'
    run(chip.dataset.url, s.conc, s.secs).then(function (r) {
      lock(false); go.textContent = 'Start'
      addRows([{ label: chip.dataset.label, r: r }], false)
    })
  })

  race.addEventListener('click', async function () {
    if (busy) return
    var s = settings(), results = []
    lock(true)
    $('board-rows').innerHTML = ''
    for (var i = 0; i < chips.length; i++) {
      var c = chips[i]
      chips.forEach(function (x) { x.classList.toggle('on', x === c) })
      race.textContent = 'Racing ' + c.dataset.label + '…'
      var r = await run(c.dataset.url, s.conc, Math.min(s.secs, 3))
      results.push({ label: c.dataset.label, r: r })
      addRows(results, true)
    }
    race.textContent = 'Race every page'
    lock(false)
  })

  var runs = []
  function addRows(rows, replace) {
    runs = replace ? rows.slice() : runs.concat(rows)
    var sorted = runs.slice().sort(function (a, b) { return b.r.rps - a.r.rps })
    var max = sorted.length ? sorted[0].r.rps || 1 : 1
    $('board').hidden = false
    $('board-rows').innerHTML = sorted.map(function (x, i) {
      return '<div class="lb"><span class="rank">' + (i + 1) + '</span><span>' + x.label +
        '</span><div class="track"><div class="fill new" style="width:0" data-w="' + (x.r.rps / max) * 100 +
        '"></div></div><span class="val">' + x.r.rps.toLocaleString('en-US') + '/s · ' + fmtMs(x.r.p50) + ' ms</span></div>'
    }).join('')
    requestAnimationFrame(function () {
      $('board-rows').querySelectorAll('.fill').forEach(function (f) { f.style.width = f.dataset.w + '%' })
    })
  }
})()
