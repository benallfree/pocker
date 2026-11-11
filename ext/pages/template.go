package pages

import (
	"os"
	"path"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// RenderTemplate renders pb_pages/_private/<name> (.ejs, .md, .jsx or .tsx) as the whole response to e (status 0:
// 200; page code can still change it), with data
// as ctx.data, for Go plugins that let a project theme their pages (e.g. ext/oauth-server's "oauth/signin"). The
// template runs like a page (ctx, include, +config.js plugins) but without middleware, layouts or +page.js, for any
// method. It reports handled=false, writing nothing, when the project has no such template.
func (p *Pages) RenderTemplate(e *core.RequestEvent, name string, status int, data map[string]any) (bool, error) {
	name = strings.Trim(path.Clean("/"+name), "/")
	if name == "" || strings.HasPrefix(name, "..") || p.Table() == nil {
		return false, nil
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return false, nil
	}
	defer root.Close()
	for _, ext := range includeExts {
		file := "_private/" + name + ext
		if st, err := root.Stat(file); err != nil || st.IsDir() {
			continue
		}
		route := &Route{Kind: KindPage, Pattern: e.Request.URL.Path, Name: path.Base(name), File: file}
		if data == nil {
			data = map[string]any{}
		}
		return true, p.renderWith(e, root, route, map[string]string{}, status, data, true)
	}
	return false, nil
}
