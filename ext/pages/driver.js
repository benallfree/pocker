// The page driver: one call per request. Go passes `rt` (request primitives and the route); this builds `ctx`,
// runs plugins' onRequest/extendContext, middleware (root first), the folder's +page.js / +api.js handlers, the
// template and its layouts, and sends the response. See SPEC.md section 3.
(function (rt) {
  var route = rt.route

  // ---- escaping, used by compiled templates ----
  var escRe = /[&<>"']/g
  var escMap = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&#34;', "'": '&#39;' }
  function str(v) {
    return v === null || v === undefined ? '' : String(v)
  }
  function esc(v) {
    return str(v).replace(escRe, function (c) {
      return escMap[c]
    })
  }

  // ---- ctx ----
  var ctx = {}
  var queryVals = rt.query()
  var cookieVals = null
  function loadCookies() {
    if (!cookieVals) cookieVals = rt.cookies()
    return cookieVals
  }

  ctx.req = {
    method: rt.method,
    url: rt.url,
    query: function (name, opts) {
      if (name === undefined) {
        var o = {}
        for (var k in queryVals) o[k] = queryVals[k][0]
        return o
      }
      var v = queryVals[name]
      if (opts && opts.all) return v ? v.slice() : []
      return v ? v[0] : undefined
    },
    header: function (name) {
      return rt.header(name)
    },
    cookies: function () {
      var c = loadCookies()
      var o = {}
      for (var k in c) o[k] = c[k]
      return o
    },
    cookie: function (name, opts) {
      var v = loadCookies()[name]
      if (v === undefined) return undefined
      if (opts && opts.json) {
        try {
          return JSON.parse(v)
        } catch (e) {
          return undefined
        }
      }
      return v
    },
    body: function () {
      return rt.body()
    },
    files: function (name) {
      return rt.files(name)
    },
    ip: rt.ip,
    event: rt.event,
  }

  function sendWith(type, a, b, conv) {
    if (b === undefined) rt.send(200, type, conv(a))
    else rt.send(a, type, conv(b))
    return ctx.res
  }
  ctx.res = {
    status: function (code) {
      rt.setStatus(code)
      return ctx.res
    },
    header: function (name, value) {
      if (value === undefined) return rt.getHeader(name)
      rt.setHeader(name, String(value))
      return ctx.res
    },
    cookie: function (name, value, opts) {
      rt.setCookie(name, typeof value === 'string' ? value : JSON.stringify(value), opts || {})
      return ctx.res
    },
    clearCookie: function (name, opts) {
      rt.clearCookie(name, opts || {})
      return ctx.res
    },
    redirect: function (location, opts) {
      opts = opts || {}
      if (opts.flash !== undefined) rt.setCookie('pp_flash', JSON.stringify(opts.flash), { maxAge: 300 })
      rt.redirect(opts.status || 302, String(location))
      return ctx.res
    },
    json: function (a, b) {
      if (b === undefined) rt.sendJSON(200, a)
      else rt.sendJSON(a, b)
      return ctx.res
    },
    html: function (a, b) {
      return sendWith('text/html; charset=utf-8', a, b, String)
    },
    text: function (a, b) {
      return sendWith('text/plain; charset=utf-8', a, b, String)
    },
    file: function (path) {
      rt.sendFile(String(path))
      return ctx.res
    },
    sse: function () {
      return rt.sse()
    },
    layout: function (on) {
      rt.setLayout(!!on)
      return ctx.res
    },
  }
  Object.defineProperty(ctx.res, 'sent', {
    get: function () {
      return rt.sent()
    },
  })

  ctx.data = {}
  ctx.params = rt.params
  ctx.route = { path: route.path, name: route.name, file: route.file }
  ctx.auth = rt.auth
  ctx.app = $app
  ctx.dev = rt.dev
  ctx.env = function (name) {
    return $os.getenv(name) || ''
  }
  ctx.log = {
    debug: function () {
      rt.log('debug', Array.prototype.slice.call(arguments))
    },
    info: function () {
      rt.log('info', Array.prototype.slice.call(arguments))
    },
    warn: function () {
      rt.log('warn', Array.prototype.slice.call(arguments))
    },
    error: function () {
      rt.log('error', Array.prototype.slice.call(arguments))
    },
  }
  ctx.meta = function (key, value) {
    if (arguments.length < 2) return rt.metaGet(key)
    rt.metaSet(key, value)
    return value
  }
  ctx.asset = function (path) {
    return rt.asset(route.file, String(path))
  }
  ctx.include = function (name, locals) {
    return rt.include(route.file, String(name), locals)
  }
  ctx.resolve = function (name, opts) {
    return rt.resolve(route.file, String(name), (opts && opts.mode) || 'require')
  }
  ctx.store = function (name, value) {
    if (value === undefined) return $app.store().get(name)
    $app.store().set(name, value)
    return value
  }
  var flashRead = false,
    flashValue
  ctx.flash = function () {
    if (!flashRead) {
      flashRead = true
      var raw = rt.flash()
      if (raw !== undefined && raw !== null) {
        try {
          flashValue = JSON.parse(raw)
        } catch (e) {
          flashValue = raw
        }
      }
    }
    return flashValue
  }
  if (rt.realtime) ctx.realtime = rt.realtime
  if (rt.session) {
    var session = rt.session
    ctx.session = {}
    Object.keys(session).forEach(function (name) {
      if (name === 'current') return
      ctx.session[name] = function () {
        var out = session[name].apply(null, arguments)
        ctx.auth = session.current()
        return out
      }
    })
  }
  var pbClient = null,
    pbToken = null
  ctx.pb = function () {
    var token = rt.authToken()
    if (pbClient && pbToken === token) return pbClient
    var Client = rt.sdk()
    var pb = new Client(rt.sdkBaseURL)
    if (token) pb.authStore.save(token, JSON.parse(rt.authRecordJSON()))
    pb.beforeSend = function (url, options) {
      options.fetch = rt.apiSend
      return { url: url, options: options }
    }
    pbClient = pb
    pbToken = token
    return pb
  }
  ctx.httpError = function (status, message) {
    var e = new Error(message === undefined ? '' : String(message))
    e.__ppHttp = { status: status, message: message === undefined ? '' : String(message) }
    return e
  }

  // ---- what compiled templates receive ----
  rt.setPP({
    ctx: ctx,
    include: rt.include,
    require: rt.require,
    asset: rt.asset,
    setSlot: rt.setSlot,
    getSlot: rt.getSlot,
    wrapped: rt.wrapped,
    esc: esc,
    str: str,
    rethrow: function (e, file, line) {
      if (e && typeof e === 'object') {
        if (!e.__ppHttp && !e.__ppWhere) {
          try {
            e.__ppWhere = file + ':' + line
          } catch (_) {}
        }
        return e
      }
      var x = new Error(String(e))
      x.__ppWhere = file + ':' + line
      return x
    },
  })

  // ---- plugins ----
  var plugins = rt.plugins()
  for (var pi = 0; pi < plugins.length; pi++) {
    if (typeof plugins[pi].extendContext === 'function') plugins[pi].extendContext(ctx)
  }
  for (pi = 0; pi < plugins.length; pi++) {
    if (typeof plugins[pi].onRequest === 'function') plugins[pi].onRequest(ctx)
    if (rt.sent()) return
  }
  function transform(html, file) {
    for (var i = 0; i < plugins.length; i++) {
      if (typeof plugins[i].transform === 'function') {
        var out = plugins[i].transform({ html: html, ctx: ctx, file: file })
        if (typeof out === 'string') html = out
      }
    }
    return html
  }

  // ---- the request ----
  var methods = ['get', 'post', 'put', 'patch', 'delete']
  function assign(v) {
    if (v && typeof v === 'object' && v !== ctx.res && !rt.sent()) {
      for (var k in v) ctx.data[k] = v[k]
    }
  }

  function handle() {
    var mod = route.handler ? rt.load(route.handler) : null
    var m = rt.method.toLowerCase()
    if (m === 'head') m = 'get'

    if (mod && typeof mod.load === 'function') {
      assign(mod.load(ctx))
      if (rt.sent()) return
    }
    var h = mod && mod[m]
    if (typeof h === 'function') {
      assign(h(ctx))
      if (rt.sent()) return
    } else if (route.kind === 'api' || m !== 'get') {
      var allow = ['GET', 'HEAD']
      for (var i = 1; i < methods.length; i++) {
        if (mod && typeof mod[methods[i]] === 'function') allow.push(methods[i].toUpperCase())
      }
      if (route.kind === 'api' && !(mod && typeof mod.get === 'function')) allow = allow.slice(2)
      rt.methodNotAllowed(allow.join(', '))
      return
    }
    if (route.kind === 'api') {
      if (!rt.sent()) throw new Error(route.file + ': the ' + m + ' handler returned without sending a response')
      return
    }

    var html = transform(rt.render(route.file, null), route.file)
    if (rt.sent()) return
    if (rt.layoutsOn()) {
      for (var li = 0; li < route.layouts.length; li++) {
        rt.setWrapped(html)
        html = transform(rt.render(route.layouts[li], null), route.layouts[li])
        if (rt.sent()) return
      }
    }
    rt.sendPage(html)
  }

  var mi = 0
  function next() {
    if (mi < route.middlewares.length) {
      var file = route.middlewares[mi++]
      var fn = rt.load(file)
      if (typeof fn !== 'function') throw new Error(file + ' must export a function (ctx, next)')
      var nextCalled = false
      var out = fn(ctx, function () {
        nextCalled = true
        return next()
      })
      if (!nextCalled && !rt.sent()) {
        throw new Error(file + ' ended the request without calling next() or sending a response')
      }
      return out
    }
    return handle()
  }
  next()
})
