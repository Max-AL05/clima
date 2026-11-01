package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config se guarda en <directorio de config del usuario>/clima-cli/config.json
// (en Linux: ~/.config/clima-cli/config.json).
type Config struct {
	CiudadFavorita string `json:"ciudad_favorita"`
	Unidades       string `json:"unidades"`
	Idioma         string `json:"idioma"`
}

func rutaConfig() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "clima-cli", "config.json"), nil
}

// CargarConfig lee la configuración; si no existe (o está dañada) usa valores por defecto.
func CargarConfig() Config {
	cfg := Config{Unidades: "metric", Idioma: "es"}
	ruta, err := rutaConfig()
	if err != nil {
		return cfg
	}
	datos, err := os.ReadFile(ruta)
	if err != nil {
		return cfg
	}
	var leida Config
	if err := json.Unmarshal(datos, &leida); err != nil {
		return cfg
	}
	if leida.Unidades != "" {
		cfg.Unidades = leida.Unidades
	}
	if leida.Idioma != "" {
		cfg.Idioma = leida.Idioma
	}
	cfg.CiudadFavorita = leida.CiudadFavorita
	return cfg
}

func (c Config) Guardar() (string, error) {
	ruta, err := rutaConfig()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(ruta), 0o755); err != nil {
		return "", err
	}
	datos, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(ruta, datos, 0o644); err != nil {
		return "", errors.New("no se pudo escribir la configuración: " + err.Error())
	}
	return ruta, nil
}
