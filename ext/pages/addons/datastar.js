// Datastar add-on for PocketPages v1 (ext/pages). https://data-star.dev
//
// Copy this file into pb_pages (for example pb_pages/_private/datastar.js) and list it in +config.js:
//
//   module.exports = { plugins: ['realtime', require('./_private/datastar.js')] }
//
// It adds ctx.datastar (see the PocketPages docs). The realtime helpers need the built-in 'realtime' plugin.
// Maintained in ext/pages/addons/datastar.js; starter kits carry a copy.

var LOADER = "<script\n  type=\"module\"\n  defer\n  src=\"https://cdn.jsdelivr.net/gh/starfederation/datastar@main/bundles/datastar.js\"\n></script>\n<script language=\"javascript\">\n  const patchSignals = (signals) => {\n    document.dispatchEvent(\n      new CustomEvent('datastar-fetch', {\n        detail: {\n          type: 'datastar-patch-signals',\n          el: null,\n          argsRaw: {\n            signals: JSON.stringify(signals),\n          },\n        },\n      })\n    )\n  }\n</script>\n"
var SPA = "<script>\n  {\n    const headers = {}\n    if ('{{selector}}') {\n      headers['Datastar-Selector'] = '{{selector}}'\n    }\n\n    // Wait for DOM to be fully loaded before adding the popstate div\n    const addPopstateDiv = () => {\n      var popstateDiv = document.createElement('div')\n      popstateDiv.setAttribute(\n        'data-on-popstate__window',\n        `url=event.state?.datastar?.url;if(!url) return;@get(url, {headers: ${JSON.stringify(headers)}})`\n      )\n      document.body.appendChild(popstateDiv)\n    }\n\n    // Add the div when DOM is ready\n    if (document.readyState === 'loading') {\n      document.addEventListener('DOMContentLoaded', addPopstateDiv)\n    } else {\n      addPopstateDiv()\n    }\n\n    document.addEventListener('DOMContentLoaded', function () {\n      var e = document.querySelector('{{scope}}')\n      if (!e) return\n      var links = e.querySelectorAll('a')\n      links.forEach(function (a) {\n        a.setAttribute(\n          'data-on-click',\n          `evt.preventDefault();e=evt.target;href=e.getAttribute('href');history.pushState({datastar: {url: href, }}, '', href);@get(href, {headers: ${JSON.stringify(headers)}})`\n        )\n      })\n    })\n  }\n</script>\n"
var REALTIME = "<script>\n  {\n    const source = new EventSource('/api/realtime') // URL to your SSE endpoint\n    source.addEventListener('PB_CONNECT', function (event) {\n      const e = JSON.parse(event.data)\n      // console.log('PB_CONNECT event:', e)\n      const { clientId } = e\n      fetch(`/api/realtime`, {\n        method: 'POST',\n        headers: {\n          'Content-Type': 'application/json',\n        },\n        body: JSON.stringify({\n          clientId,\n          subscriptions: ['datastar'],\n        }),\n      }).catch(console.error)\n\n      patchSignals({ clientId })\n    })\n\n    source.addEventListener('datastar', function (event) {\n      const e = JSON.parse(event.data)\n      // console.log('datastar event:', e)\n      document.dispatchEvent(\n        new CustomEvent('datastar-fetch', {\n          detail: e,\n        })\n      )\n    })\n  }\n</script>\n"

var PATCH_ELEMENTS = 'datastar-patch-elements'
var PATCH_SIGNALS = 'datastar-patch-signals'
var MODES = ['outer', 'inner', 'replace', 'prepend', 'append', 'before', 'after', 'remove']

function elementLines(html, opts) {
  var lines = []
  if (opts.selector) lines.push('selector ' + opts.selector)
  if (opts.mode && opts.mode !== 'outer') {
    if (MODES.indexOf(opts.mode) < 0) throw new Error('datastar: unknown mode ' + opts.mode)
    lines.push('mode ' + opts.mode)
  }
  if (opts.useViewTransition) lines.push('useViewTransition true')
  if (html) {
    String(html)
      .split('\n')
      .forEach(function (l) {
        lines.push('elements ' + l)
      })
  }
  return lines
}

function signalLines(signals, opts) {
  var lines = []
  if (opts.onlyIfMissing) lines.push('onlyIfMissing true')
  var text = typeof signals === 'string' ? signals : JSON.stringify(signals)
  text.split('\n').forEach(function (l) {
    lines.push('signals ' + l)
  })
  return lines
}

function scriptTag(js, opts) {
  var tag = '<script'
  ;(opts.attributes || []).forEach(function (a) {
    tag += ' ' + a
  })
  if (opts.autoRemove === undefined || opts.autoRemove === null || opts.autoRemove) tag += ' data-effect="el.remove()"'
  return tag + '>' + js + '</script>'
}

module.exports = function () {
  return {
    name: 'datastar',
    extendContext: function (ctx) {
      var stream = null
      function send(event, lines, opts) {
        if (!stream) stream = ctx.res.sse()
        stream.send(event, lines.join('\n'), { id: opts.eventId, retry: opts.retryDuration })
      }
      function broadcast(type, argsRaw, realtimeOpts) {
        if (!ctx.realtime) throw new Error("datastar: the realtime helpers need the 'realtime' plugin")
        ctx.realtime.send('datastar', { type: type, el: null, argsRaw: argsRaw }, realtimeOpts || {})
      }
      var ds = {
        scripts: function (opts) {
          opts = opts || {}
          var out = [LOADER]
          if (opts.spa) {
            out.push(SPA.split('{{scope}}').join(opts.spa.scope).split('{{selector}}').join(opts.spa.selector || ''))
          }
          if (opts.realtime) out.push(REALTIME)
          return out.join('\n')
        },
        patchElements: function (html, opts) {
          opts = opts || {}
          send(PATCH_ELEMENTS, elementLines(html, opts), opts)
        },
        patchSignals: function (signals, opts) {
          opts = opts || {}
          send(PATCH_SIGNALS, signalLines(signals, opts), opts)
        },
        executeScript: function (js, opts) {
          opts = opts || {}
          ds.patchElements(scriptTag(js, opts), { selector: 'body', mode: 'append', eventId: opts.eventId, retryDuration: opts.retryDuration })
        },
        signals: function () {
          var raw
          if (ctx.req.method === 'GET') {
            raw = ctx.req.query('datastar')
            if (!raw) return {}
            try {
              return JSON.parse(raw)
            } catch (e) {
              throw new Error('datastar: bad signals: ' + e)
            }
          }
          var body = ctx.req.body()
          if (typeof body === 'string') {
            try {
              return JSON.parse(body)
            } catch (e) {
              throw new Error('datastar: bad signals: ' + e)
            }
          }
          return body || {}
        },
        realtime: {
          patchElements: function (html, opts, realtimeOpts) {
            var args = { elements: html }
            for (var k in opts || {}) args[k] = opts[k]
            broadcast(PATCH_ELEMENTS, args, realtimeOpts)
          },
          patchSignals: function (signals, opts, realtimeOpts) {
            var args = { signals: typeof signals === 'string' ? signals : JSON.stringify(signals) }
            for (var k in opts || {}) args[k] = opts[k]
            broadcast(PATCH_SIGNALS, args, realtimeOpts)
          },
          executeScript: function (js, realtimeOpts) {
            broadcast(PATCH_ELEMENTS, { elements: scriptTag(js, {}), selector: 'body', mode: 'append' }, realtimeOpts)
          },
        },
      }
      ctx.datastar = ds
    },
    // A Datastar request gets the rendered page (before layouts) back as a patch.
    transform: function (args) {
      var ctx = args.ctx
      if (args.file !== ctx.route.file || ctx.req.header('Datastar-Request') !== 'true' || ctx.res.sent) return args.html
      var selector = ctx.req.header('Datastar-Selector')
      ctx.datastar.patchElements(args.html, selector ? { selector: selector, mode: 'inner' } : {})
      return args.html
    },
  }
}
