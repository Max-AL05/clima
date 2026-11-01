package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// Cache guarda respuestas de la API en archivos JSON dentro del directorio de caché del usuario.
// Todos los métodos son seguros de llamar sobre un *Cache nil (simplemente no hacen nada).
type Cache struct {
	Dir string
	TTL time.Duration
}

func NuevoCache(ttl time.Duration) (*Cache, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return NuevoCacheEn(filepath.Join(base, "clima-cli"), ttl)
}

func NuevoCacheEn(dir string, ttl time.Duration) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{Dir: dir, TTL: ttl}, nil
}

func (c *Cache) ruta(clave string) string {
	suma := sha256.Sum256([]byte(clave))
	return filepath.Join(c.Dir, hex.EncodeToString(suma[:])+".json")
}

func (c *Cache) Get(clave string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	ruta := c.ruta(clave)
	info, err := os.Stat(ruta)
	if err != nil || time.Since(info.ModTime()) > c.TTL {
		return nil, false
	}
	datos, err := os.ReadFile(ruta)
	if err != nil {
		return nil, false
	}
	return datos, true
}

func (c *Cache) Set(clave string, datos []byte) {
	if c == nil {
		return
	}
	// Escritura atómica: archivo temporal + rename, para que otra goroutine
	// no lea un archivo a medio escribir.
	tmp, err := os.CreateTemp(c.Dir, "tmp-*")
	if err != nil {
		return
	}
	_, errEscribir := tmp.Write(datos)
	errCerrar := tmp.Close()
	if errEscribir != nil || errCerrar != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), c.ruta(clave)); err != nil {
		os.Remove(tmp.Name())
	}
}
