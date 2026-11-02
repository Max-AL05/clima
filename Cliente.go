package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const urlBaseDefecto = "https://api.openweathermap.org/data/2.5"

// Errores "conocidos": se pueden comprobar con errors.Is.
var (
	ErrCiudadNoEncontrada = errors.New("ciudad no encontrada")
	ErrAPIKeyInvalida     = errors.New("API key inválida (o todavía no activada)")
	ErrLimiteExcedido     = errors.New("límite de peticiones excedido, intenta más tarde")
)

// ---------- Modelos ----------

type Condicion struct {
	Description string `json:"description"`
}

type Clima struct {
	Name     string `json:"name"`
	Timezone int    `json:"timezone"` // segundos de diferencia con UTC
	Coord    struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"coord"`
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		TempMin   float64 `json:"temp_min"`
		TempMax   float64 `json:"temp_max"`
		Pressure  int     `json:"pressure"`
		Humidity  int     `json:"humidity"`
	} `json:"main"`
	Weather []Condicion `json:"weather"`
	Wind    struct {
		Speed float64 `json:"speed"`
	} `json:"wind"`
	Sys struct {
		Country string `json:"country"`
		Sunrise int64  `json:"sunrise"`
		Sunset  int64  `json:"sunset"`
	} `json:"sys"`
}

// Descripcion devuelve la primera condición climática (o "" si no hay).
func (c *Clima) Descripcion() string {
	if len(c.Weather) > 0 {
		return c.Weather[0].Description
	}
	return ""
}

type respuestaPronostico struct {
	City struct {
		Timezone int `json:"timezone"`
	} `json:"city"`
	List []struct {
		Dt   int64 `json:"dt"`
		Main struct {
			Temp     float64 `json:"temp"`
			Humidity int     `json:"humidity"`
		} `json:"main"`
		Weather []Condicion `json:"weather"`
		Wind    struct {
			Speed float64 `json:"speed"`
		} `json:"wind"`
		Pop float64 `json:"pop"` // probabilidad de lluvia 0..1
	} `json:"list"`
}

// DiaPronostico resume las entradas de 3 horas de un mismo día.
type DiaPronostico struct {
	Fecha       string   `json:"fecha"` // AAAA-MM-DD (hora local de la ciudad)
	TempMin     float64  `json:"temp_min"`
	TempMax     float64  `json:"temp_max"`
	Descripcion string   `json:"descripcion"`
	ProbLluvia  float64  `json:"prob_lluvia"` // 0..100
	UVMax       *float64 `json:"uv_max,omitempty"`
}

// PuntoHora es una lectura del pronóstico (cada 3 horas) con todas las series que se pueden graficar.
type PuntoHora struct {
	Hora       string   `json:"hora"` // "2006-01-02T15:04", hora local de la ciudad
	Temp       float64  `json:"temp"`
	Humedad    int      `json:"humedad"`     // %
	Viento     float64  `json:"viento"`      // m/s (mph si las unidades son imperial)
	ProbLluvia float64  `json:"prob_lluvia"` // 0..100
	UV         *float64 `json:"uv,omitempty"`
}

// horasGrafica: 8 lecturas de 3 horas = las próximas 24 horas.
const horasGrafica = 8

// extraerHoras devuelve las primeras n lecturas del pronóstico, con la hora local de la ciudad.
func extraerHoras(r *respuestaPronostico, n int) []PuntoHora {
	offset := time.Duration(r.City.Timezone) * time.Second
	var puntos []PuntoHora
	for _, e := range r.List {
		if len(puntos) == n {
			break
		}
		hora := time.Unix(e.Dt, 0).UTC().Add(offset).Format("2006-01-02T15:04")
		puntos = append(puntos, PuntoHora{
			Hora:       hora,
			Temp:       e.Main.Temp,
			Humedad:    e.Main.Humidity,
			Viento:     e.Wind.Speed,
			ProbLluvia: e.Pop * 100,
		})
	}
	return puntos
}

// agruparPorDia convierte la lista de pronósticos cada 3 horas en un resumen diario.
func agruparPorDia(r *respuestaPronostico) []DiaPronostico {
	var dias []DiaPronostico
	indice := map[string]int{}
	conteos := map[string]map[string]int{} // fecha -> descripción -> veces
	orden := map[string][]string{}         // fecha -> descripciones en orden de aparición

	offset := time.Duration(r.City.Timezone) * time.Second
	for _, e := range r.List {
		fecha := time.Unix(e.Dt, 0).UTC().Add(offset).Format("2006-01-02")
		i, existe := indice[fecha]
		if !existe {
			dias = append(dias, DiaPronostico{Fecha: fecha, TempMin: e.Main.Temp, TempMax: e.Main.Temp})
			i = len(dias) - 1
			indice[fecha] = i
			conteos[fecha] = map[string]int{}
		}
		d := &dias[i]
		if e.Main.Temp < d.TempMin {
			d.TempMin = e.Main.Temp
		}
		if e.Main.Temp > d.TempMax {
			d.TempMax = e.Main.Temp
		}
		if pop := e.Pop * 100; pop > d.ProbLluvia {
			d.ProbLluvia = pop
		}
		if len(e.Weather) > 0 {
			desc := e.Weather[0].Description
			if conteos[fecha][desc] == 0 {
				orden[fecha] = append(orden[fecha], desc)
			}
			conteos[fecha][desc]++
		}
	}

	// La descripción del día es la que más se repite.
	for i := range dias {
		mejor, max := "", 0
		for _, desc := range orden[dias[i].Fecha] {
			if n := conteos[dias[i].Fecha][desc]; n > max {
				mejor, max = desc, n
			}
		}
		dias[i].Descripcion = mejor
	}
	return dias
}

// ---------- Cliente ----------

type Cliente struct {
	BaseURL  string
	BaseUV   string // servicio de UV (Open-Meteo); si está vacío se usa el de producción
	APIKey   string
	Unidades string // metric | imperial | standard
	Idioma   string // es, en, fr...
	HTTP     *http.Client
	Cache    *Cache // puede ser nil
}

func NuevoCliente(apiKey, unidades, idioma string, timeout time.Duration, cache *Cache) *Cliente {
	return &Cliente{
		BaseURL:  urlBaseDefecto,
		BaseUV:   urlUVDefecto,
		APIKey:   apiKey,
		Unidades: unidades,
		Idioma:   idioma,
		HTTP:     &http.Client{Timeout: timeout},
		Cache:    cache,
	}
}

// sinURL quita la URL de un *url.Error: la URL puede contener la API key.
func sinURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// peticion hace un GET (o usa la caché) y decodifica el JSON en destino.
// mapear convierte un código HTTP distinto de 200 en un error con sentido.
func (c *Cliente) peticion(ctx context.Context, rawURL, claveCache string, destino any, mapear func(codigo int) error) error {
	if datos, ok := c.Cache.Get(claveCache); ok {
		if err := json.Unmarshal(datos, destino); err == nil {
			return nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("error al crear la petición: %w", sinURL(err))
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("error de conexión: %w", sinURL(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return mapear(resp.StatusCode)
	}

	// Decodificamos directo del body y, a la vez, guardamos una copia para la caché.
	var copia bytes.Buffer
	if err := json.NewDecoder(io.TeeReader(resp.Body, &copia)).Decode(destino); err != nil {
		return fmt.Errorf("error al procesar el JSON: %w", err)
	}
	c.Cache.Set(claveCache, copia.Bytes())
	return nil
}

// obtener consulta un endpoint de OpenWeatherMap para una ciudad.
func (c *Cliente) obtener(ctx context.Context, endpoint, ciudad string, destino any) error {
	params := url.Values{}
	params.Set("q", ciudad)
	params.Set("units", c.Unidades)
	params.Set("lang", c.Idioma)

	// La clave de caché NO incluye la API key.
	claveCache := endpoint + "?" + strings.ToLower(params.Encode())

	params.Set("appid", c.APIKey)
	rawURL := c.BaseURL + "/" + endpoint + "?" + params.Encode()

	return c.peticion(ctx, rawURL, claveCache, destino, func(codigo int) error {
		switch codigo {
		case http.StatusNotFound:
			return fmt.Errorf("%w: '%s'", ErrCiudadNoEncontrada, ciudad)
		case http.StatusUnauthorized:
			return ErrAPIKeyInvalida
		case http.StatusTooManyRequests:
			return ErrLimiteExcedido
		}
		return fmt.Errorf("error inesperado de la API (código %d)", codigo)
	})
}

func (c *Cliente) ClimaActual(ctx context.Context, ciudad string) (*Clima, error) {
	var clima Clima
	if err := c.obtener(ctx, "weather", ciudad, &clima); err != nil {
		return nil, err
	}
	return &clima, nil
}

// Pronostico devuelve el resumen por día y las lecturas de las próximas 24 horas.
func (c *Cliente) Pronostico(ctx context.Context, ciudad string) ([]DiaPronostico, []PuntoHora, error) {
	var r respuestaPronostico
	if err := c.obtener(ctx, "forecast", ciudad, &r); err != nil {
		return nil, nil, err
	}
	return agruparPorDia(&r), extraerHoras(&r, horasGrafica), nil
}

// ---------- Consultas concurrentes ----------

type Resultado struct {
	Ciudad     string          `json:"ciudad"`
	Unidades   string          `json:"unidades"`
	Clima      *Clima          `json:"clima,omitempty"`
	Pronostico []DiaPronostico `json:"pronostico,omitempty"`
	Horas      []PuntoHora     `json:"proximas_horas,omitempty"`
	UV         *UV             `json:"uv,omitempty"`
	Aviso      string          `json:"aviso,omitempty"` // problema no fatal (p. ej. UV no disponible)
	Error      string          `json:"error,omitempty"`
	err        error
}

const maxConcurrentes = 5

// Opciones indica qué datos opcionales se piden además del clima actual.
type Opciones struct {
	Pronostico bool // pronóstico por día y próximas 24 horas
	UV         bool // índice UV (servicio Open-Meteo)
}

// consultarCiudades consulta todas las ciudades en paralelo (máx. 5 a la vez).
// El resultado conserva el mismo orden que la lista de entrada.
func consultarCiudades(ctx context.Context, cli *Cliente, ciudades []string, op Opciones) []Resultado {
	resultados := make([]Resultado, len(ciudades))
	sem := make(chan struct{}, maxConcurrentes)
	var wg sync.WaitGroup

	for i, ciudad := range ciudades {
		wg.Add(1)
		go func(i int, ciudad string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r := Resultado{Ciudad: ciudad, Unidades: cli.Unidades}
			r.Clima, r.err = cli.ClimaActual(ctx, ciudad)
			if r.err == nil && op.Pronostico {
				r.Pronostico, r.Horas, r.err = cli.Pronostico(ctx, ciudad)
			}
			if r.err != nil {
				r.Error = r.err.Error()
				r.Clima, r.Pronostico, r.Horas = nil, nil, nil
				resultados[i] = r
				return
			}

			// El UV viene de otro servicio: si falla, avisamos pero no descartamos el resto.
			if op.UV {
				lat, lon := r.Clima.Coord.Lat, r.Clima.Coord.Lon
				if lat == 0 && lon == 0 {
					r.Aviso = "índice UV no disponible: la API no devolvió coordenadas"
				} else if d, err := cli.ConsultarUV(ctx, lat, lon); err != nil {
					r.Aviso = "índice UV no disponible: " + err.Error()
				} else {
					r.aplicarUV(d)
				}
			}
			resultados[i] = r // cada goroutine escribe en su propia posición: sin data race
		}(i, ciudad)
	}
	wg.Wait()
	return resultados
}