package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ansiReset = "\033[0m"
	ansiAzul  = "\033[34m"
	ansiVerde = "\033[32m"
	ansiRojo  = "\033[31m"
	ansiGris  = "\033[90m"
)

func pintar(activo bool, codigo, texto string) string {
	if !activo {
		return texto
	}
	return codigo + texto + ansiReset
}

func simboloTemp(unidades string) string {
	switch unidades {
	case "imperial":
		return "°F"
	case "standard":
		return " K"
	}
	return "°C"
}

func unidadViento(unidades string) string {
	if unidades == "imperial" {
		return "mph"
	}
	return "m/s"
}

func aCelsius(t float64, unidades string) float64 {
	switch unidades {
	case "imperial":
		return (t - 32) * 5 / 9
	case "standard":
		return t - 273.15
	}
	return t
}

// colorTemp: azul si hace frío, verde si es templado, rojo si hace calor.
func colorTemp(t float64, unidades string) string {
	c := aCelsius(t, unidades)
	switch {
	case c < 10:
		return ansiAzul
	case c <= 25:
		return ansiVerde
	default:
		return ansiRojo
	}
}

// horaLocal formatea un timestamp Unix en la hora local de la ciudad.
func horaLocal(ts int64, tz int) string {
	if ts == 0 {
		return "--:--"
	}
	return time.Unix(ts, 0).UTC().Add(time.Duration(tz) * time.Second).Format("15:04")
}

func capitalizar(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func mostrarClima(w io.Writer, c *Clima, unidades string, color bool) {
	sim := simboloTemp(unidades)
	lugar := c.Name
	if c.Sys.Country != "" {
		lugar += ", " + c.Sys.Country
	}

	temp := fmt.Sprintf("%.1f%s", c.Main.Temp, sim)
	fmt.Fprintf(w, "\n--- Clima actual en %s ---\n", lugar)
	fmt.Fprintf(w, "  Temperatura:  %s (sensación %.1f%s)\n",
		pintar(color, colorTemp(c.Main.Temp, unidades), temp), c.Main.FeelsLike, sim)
	fmt.Fprintf(w, "  Mín / Máx:    %.1f%s / %.1f%s\n", c.Main.TempMin, sim, c.Main.TempMax, sim)
	fmt.Fprintf(w, "  Humedad:      %d%%\n", c.Main.Humidity)
	fmt.Fprintf(w, "  Presión:      %d hPa\n", c.Main.Pressure)
	fmt.Fprintf(w, "  Viento:       %.1f %s\n", c.Wind.Speed, unidadViento(unidades))
	if d := c.Descripcion(); d != "" {
		fmt.Fprintf(w, "  Condición:    %s\n", capitalizar(d))
	}
	fmt.Fprintf(w, "  Amanecer:     %s    Atardecer: %s (hora local)\n",
		horaLocal(c.Sys.Sunrise, c.Timezone), horaLocal(c.Sys.Sunset, c.Timezone))
	fmt.Fprintln(w, "-----------------------------")
}

var nombresDia = [...]string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}

func mostrarPronostico(w io.Writer, dias []DiaPronostico, unidades string, color bool) {
	sim := simboloTemp(unidades)
	fmt.Fprintln(w, "Pronóstico:")
	for _, d := range dias {
		etiqueta := d.Fecha
		if t, err := time.Parse("2006-01-02", d.Fecha); err == nil {
			etiqueta = fmt.Sprintf("%s %s", nombresDia[t.Weekday()], t.Format("02/01"))
		}
		min := pintar(color, colorTemp(d.TempMin, unidades), fmt.Sprintf("%5.1f%s", d.TempMin, sim))
		max := pintar(color, colorTemp(d.TempMax, unidades), fmt.Sprintf("%5.1f%s", d.TempMax, sim))
		fmt.Fprintf(w, "  %s  %s / %s  ☔ %3.0f%%  %s\n",
			etiqueta, min, max, d.ProbLluvia, pintar(color, ansiGris, capitalizar(d.Descripcion)))
	}
	fmt.Fprintln(w, "-----------------------------")
}

// ---------- Gráfica de temperatura ----------

var bloques = []rune("▁▂▃▄▅▆▇█")

const (
	anchoBarra   = 4
	anchoColumna = anchoBarra + 2
	margenIzq    = 8 // ancho de la etiqueta del eje: "  21.5 │"
)

// centrar centra s en un campo de ancho n (contando caracteres, no bytes).
func centrar(s string, n int) string {
	falta := n - utf8.RuneCountInString(s)
	if falta <= 0 {
		return s
	}
	izq := falta / 2
	return strings.Repeat(" ", izq) + s + strings.Repeat(" ", falta-izq)
}

// graficaTemperatura dibuja una gráfica de barras verticales con bloques Unicode.
// Cada fila tiene resolución de 1/8 de carácter, así que alto=6 da 48 niveles.
// El eje vertical NO empieza en cero: va de la temperatura mínima a la máxima.
func graficaTemperatura(puntos []PuntoHora, unidades string, color bool, alto int) string {
	if len(puntos) == 0 || alto < 1 {
		return ""
	}

	min, max := puntos[0].Temp, puntos[0].Temp
	for _, p := range puntos {
		min, max = math.Min(min, p.Temp), math.Max(max, p.Temp)
	}

	// Nivel de cada barra: entre 1 (la más baja) y alto*8 (la más alta).
	total := alto * 8
	niveles := make([]int, len(puntos))
	for i, p := range puntos {
		if max == min {
			niveles[i] = total / 2
		} else {
			niveles[i] = 1 + int(math.Round((p.Temp-min)/(max-min)*float64(total-1)))
		}
	}

	var b strings.Builder
	for fila := alto; fila >= 1; fila-- {
		switch fila {
		case alto:
			fmt.Fprintf(&b, "%6.1f │", max)
		case 1:
			fmt.Fprintf(&b, "%6.1f │", min)
		default:
			b.WriteString("       │")
		}

		base := (fila - 1) * 8
		for i, n := range niveles {
			celda := ' '
			switch {
			case n >= base+8:
				celda = '█'
			case n > base:
				celda = bloques[n-base-1]
			}
			barra := strings.Repeat(string(celda), anchoBarra)
			if celda != ' ' {
				barra = pintar(color, colorTemp(puntos[i].Temp, unidades), barra)
			}
			b.WriteString(" " + barra + " ")
		}
		b.WriteByte('\n')
	}

	// Eje horizontal, temperaturas y horas.
	b.WriteString("       └" + strings.Repeat("─", anchoColumna*len(puntos)) + "\n")
	b.WriteString(strings.Repeat(" ", margenIzq))
	for _, p := range puntos {
		b.WriteString(centrar(fmt.Sprintf("%.0f", p.Temp), anchoColumna))
	}
	b.WriteByte('\n')
	b.WriteString(strings.Repeat(" ", margenIzq))
	for _, p := range puntos {
		etiqueta := p.Hora
		if t, err := time.Parse("2006-01-02T15:04", p.Hora); err == nil {
			etiqueta = t.Format("15") + "h"
		}
		b.WriteString(centrar(etiqueta, anchoColumna))
	}
	b.WriteByte('\n')
	return b.String()
}

func mostrarGrafica(w io.Writer, puntos []PuntoHora, unidades string, color bool) {
	if len(puntos) == 0 {
		return
	}
	fmt.Fprintf(w, "Temperatura (%s), próximas 24 horas:\n", strings.TrimSpace(simboloTemp(unidades)))
	fmt.Fprint(w, graficaTemperatura(puntos, unidades, color, 6))
	fmt.Fprintln(w, "-----------------------------")
}

// ---------- Salida JSON ----------

// imprimirJSON escribe los resultados como JSON indentado.
// Solo incluye el pronóstico diario y/o las horas si se pidieron.
func imprimirJSON(w io.Writer, resultados []Resultado, conPronostico, conGrafica bool) error {
	for i := range resultados {
		if !conPronostico {
			resultados[i].Pronostico = nil
		}
		if !conGrafica {
			resultados[i].Horas = nil
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resultados)
}
