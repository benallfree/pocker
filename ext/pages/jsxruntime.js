// The JSX runtime for .jsx/.tsx pages (SPEC 4.5): __h(tag, props, ...children) renders straight to an HTML string,
// wrapped as { __html } so it isn't escaped again when nested. Evaluated once per VM; returns the runtime object.
;(function (esc) {
  var VOID = {
    area: 1, base: 1, br: 1, col: 1, embed: 1, hr: 1, img: 1, input: 1,
    link: 1, meta: 1, source: 1, track: 1, wbr: 1,
  }
  var RENAME = { className: 'class', htmlFor: 'for' }

  function Raw(s) {
    this.__html = s
  }
  Raw.prototype.toString = function () {
    return this.__html
  }
  function raw(s) {
    return new Raw(s === undefined || s === null ? '' : String(s))
  }

  function toHTML(v) {
    if (v === null || v === undefined || v === true || v === false) return ''
    if (v instanceof Raw) return v.__html
    if (Array.isArray(v)) {
      var out = ''
      for (var i = 0; i < v.length; i++) out += toHTML(v[i])
      return out
    }
    if (typeof v === 'object' && typeof v.__html === 'string') return v.__html
    return esc(String(v))
  }

  function styleString(o) {
    var out = []
    for (var k in o) {
      var v = o[k]
      if (v === null || v === undefined || v === false) continue
      var name = k.indexOf('--') === 0 ? k : k.replace(/[A-Z]/g, function (c) { return '-' + c.toLowerCase() })
      out.push(name + ':' + v)
    }
    return out.join(';')
  }

  function Fragment(props) {
    return raw(toHTML(props && props.children))
  }

  function h(tag, props) {
    var children = []
    for (var i = 2; i < arguments.length; i++) children.push(arguments[i])

    if (typeof tag === 'function') {
      var p = {}
      for (var k in props) p[k] = props[k]
      if (children.length) p.children = children.length === 1 ? children[0] : children
      return tag(p)
    }

    var attrs = ''
    var inner = null
    for (var name in props) {
      var v = props[name]
      if (name === 'children' || name === 'key' || name === 'ref') continue
      if (name === 'dangerouslySetInnerHTML') {
        inner = v && v.__html !== undefined ? String(v.__html) : ''
        continue
      }
      if (v === null || v === undefined || v === false || typeof v === 'function') continue
      var attr = RENAME[name] || name
      if (v === true) {
        attrs += ' ' + attr
        continue
      }
      if (attr === 'style' && typeof v === 'object') v = styleString(v)
      attrs += ' ' + attr + '="' + esc(String(v)) + '"'
    }
    if (VOID[tag]) return raw('<' + tag + attrs + '>')
    if (inner === null) inner = toHTML(children.length ? children : props && props.children)
    // JSX can't spell a doctype, so <html> brings its own
    var doctype = tag === 'html' ? '<!doctype html>' : ''
    return raw(doctype + '<' + tag + attrs + '>' + inner + '</' + tag + '>')
  }

  return { h: h, Fragment: Fragment, raw: raw, toHTML: toHTML }
})
