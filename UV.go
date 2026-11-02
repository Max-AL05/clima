package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// El índice UV viene de Open-Meteo (https://open-meteo.com): gratuito para uso no comercial
// y sin API key. OpenWeatherMap retiró su API de UV en 2021 y ahora solo la ofrece en
// One Call 3.0, que exige una suscripción con tarjeta de crédito.
const urlUVDefecto = "https://api.open-meteo.com/v1"

// UV resume el índice UV de una ciudad.
type UV struct {
	Actual    float64 `json:"actual"`
	Categoria string  `json:"categoria"`
	MaxHoy    float64 `json:"max_hoy"`
}

type respuestaUV struct {
	Current struct {
		Time    string  `json:"time"`
		UVIndex float64 `json:"uv_index"`
	} `json:"current"`
	Hourly struct {
		Time    []string  `json:"time"`
		UVIndex []float64 `json:"uv_index"`
	} `json:"hourly"`
	Daily struct {
		Time       []string  `json:"time"`
		UVIndexMax []float64 `json:"uv_index_max"`
	} `json:"daily"`
}

// datosUV es la respuesta ya organizada: las fechas y horas son locales de la ciudad.
type datosUV struct {
	Actual  float64
	MaxHoy  float64
	Diario  map[string]float64 // "2006-01-02" -> UV máximo del día
	Horario map[string]float64 // "2006-01-02T15:00" -> UV de esa hora
}

// categoriaUV usa la escala de la OMS.
func categoriaUV(v float64) string {
	switch {
	case v < 3:
		return "Bajo"
	case v < 6:
		return "Moderado"
	case v < 8:
		return "Alto"
	case v < 11:
		return "Muy alto"
	}
	return "Extremo"
}

func recomendacionUV(v float64) string {
	switch {
	case v < 3:
		return "no necesitas protección especial."
	case v < 6:
		return "usa protector solar y gafas; busca sombra al mediodía."
	case v < 8:
		return "protector, sombrero y gafas; evita el sol de 12 a 16 h."
	case v < 11:
		return "protección extra; evita exponerte al sol del mediodía."
	}
	return "evita el sol del mediodía y usa protección máxima."
}

// ConsultarUV pide a Open-Meteo el UV actual, el máximo diario y el UV por hora.
func (c *Cliente) ConsultarUV(ctx context.Context, lat, lon float64) (*datosUV, error) {
	params := url.Values{}
	// Dos decimales (~1 km) bastan para el UV y hacen que la caché se reutilice.
	params.Set("latitude", fmt.Sprintf("%.2f", lat))
	params.Set("longitude", fmt.Sprintf("%.2f", lon))
	params.Set("current", "uv_index")
	params.Set("hourly", "uv_index")
	params.Set("daily", "uv_index_max")
	params.Set("timezone", "auto") // fechas y horas locales del lugar
	params.Set("forecast_days", "6")

	base := c.BaseUV
	if base == "" {
		base = urlUVDefecto
	}

	var r respuestaUV
	err := c.peticion(ctx, base+"/forecast?"+params.Encode(), "uv?"+params.Encode(), &r, func(codigo int) error {
		if codigo == http.StatusTooManyRequests {
			return ErrLimiteExcedido
		}
		return fmt.Errorf("el servicio de UV respondió con código %d", codigo)
	})
	if err != nil {
		return nil, err
	}
	if r.Current.Time == "" && len(r.Daily.Time) == 0 {
		return nil, errors.New("el servicio de UV devolvió una respuesta vacía")
	}

	d := &datosUV{
		Actual:  r.Current.UVIndex,
		Diario:  map[string]float64{},
		Horario: map[string]float64{},
	}
	for i := 0; i < len(r.Daily.Time) && i < len(r.Daily.UVIndexMax); i++ {
		d.Diario[r.Daily.Time[i]] = r.Daily.UVIndexMax[i]
	}
	for i := 0; i < len(r.Hourly.Time) && i < len(r.Hourly.UVIndex); i++ {
		d.Horario[r.Hourly.Time[i]] = r.Hourly.UVIndex[i]
	}
	if len(r.Current.Time) >= 10 {
		d.MaxHoy = d.Diario[r.Current.Time[:10]]
	}
	return d, nil
}

// aplicarUV reparte los datos de UV en el resultado: resumen, pronóstico diario y horas.
func (r *Resultado) aplicarUV(d *datosUV) {
	r.UV = &UV{Actual: d.Actual, Categoria: categoriaUV(d.Actual), MaxHoy: d.MaxHoy}

	for i := range r.Pronostico {
		if v, ok := d.Diario[r.Pronostico[i].Fecha]; ok {
			r.Pronostico[i].UVMax = &v
		}
	}
	for i := range r.Horas {
		h := r.Horas[i].Hora // "2006-01-02T15:04" -> "2006-01-02T15:00"
		if len(h) < 13 {
			continue
		}
		if v, ok := d.Horario[h[:13]+":00"]; ok {
			r.Horas[i].UV = &v
		}
	}
}