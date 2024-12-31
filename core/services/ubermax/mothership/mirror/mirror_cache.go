package mirror

import (
	"fmt"
	"log/slog"
	"pocker/core/syncx"

	"github.com/pluja/pocketbase"
)

type MirrorCache[T syncx.IIndexedCacheItem] struct {
	collectionName string
	fields         []string
	client         *pocketbase.Client
	stream         *pocketbase.TypedStream[T]
	debug          bool
	cache          *syncx.IndexedCache[T]
	factory        func() T
}

type MirrorCacheConfig[T syncx.IIndexedCacheItem] struct {
	Client         *pocketbase.Client
	CollectionName string
	Fields         []string
	Debug          bool
	Factory        func() T
}

func newMirrorCache[T syncx.IIndexedCacheItem](config MirrorCacheConfig[T]) *MirrorCache[T] {
	fields := config.Fields
	if fields == nil {
		fields = []string{"id"}
	}
	mirror := &MirrorCache[T]{
		collectionName: config.CollectionName,
		fields:         fields,
		client:         config.Client,
		debug:          config.Debug,
		cache: syncx.NewIndexedCache[T](syncx.IndexedCacheConfig{
			Debug: config.Debug,
		}),
		factory: config.Factory,
	}
	return mirror

}

func (p *MirrorCache[T]) StartMirroring() chan *pocketbase.TypedEvent[T] {
	slog.Info("Starting mirroring", slog.String("collection_name", p.collectionName))
	collection := pocketbase.NewCollectionWithFactory[T](p.client, p.collectionName, p.factory)

	if p.collectionName == "" {
		slog.Error("collection name is required")
	}

	stream, err := collection.Subscribe(pocketbase.WithTarget(p.collectionName, pocketbase.WithFields(p.fields...)))
	if err != nil {
		slog.Error("Failed to subscribe", slog.String("collection_name", p.collectionName), slog.Any("error", err))
	}
	p.stream = stream

	ch := make(chan *pocketbase.TypedEvent[T])

	go func() {
		for e := range stream.C {
			fmt.Printf("event: %+v\n", e)
			if p.debug {
				slog.Debug("event", slog.String("collection_name", p.collectionName), slog.Any("event", e))
			}
			switch e.Action {
			case "create", "update":
				slog.Debug("upserting", slog.String("collection_name", p.collectionName), slog.Any("record", e.Record))
				p.cache.Upsert(e.Record)
				if p.debug {
					slog.Debug("upserted", slog.String("collection_name", p.collectionName), slog.Any("action", e.Action), slog.Any("record", e.Record))
				}
				ch <- e
			case "delete":
				id, ok := (*e.Fields)["id"]
				if !ok {
					slog.Error("delete event for %s has no id", slog.String("collection_name", p.collectionName))
					continue
				}
				p.cache.DeleteByFieldNameAndValue("id", id.(string))
				if p.debug {
					slog.Debug("deleted", slog.String("collection_name", p.collectionName), slog.String("id", id.(string)))
				}
				ch <- e
			}
		}
	}()
	return ch

}

func (p *MirrorCache[T]) Range(fn func(item T) bool) {
	p.cache.Range(fn)
}
