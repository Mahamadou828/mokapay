package http

import "fmt"

type Group struct {
	path     string
	register func(method, path string, h HandlerFunc, mw ...MidFunc)
	mw       []MidFunc
}

func (g *Group) RegisterHandler(method, path string, h HandlerFunc) {
	g.register(method, fmt.Sprintf("%s%s", g.path, path), h, g.mw...)
	return
}

func (g *Group) NewGroup(path string, mw ...MidFunc) *Group {
	middlewares := append(g.mw, mw...)
	return &Group{
		path:     fmt.Sprintf("%s%s", g.path, path),
		register: g.register,
		mw:       middlewares,
	}
}
