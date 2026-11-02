package main

import (
	"encoding/json"
	"errors"
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

	ansiAmarillo = "\033[33m"
	ansiMagenta  = "\033[35m"
	ansiCian     = "\033[36m"
	ansiRojoVivo = "\033[91m"
)

// colorUV: un color por categoría de la escala de la OMS.
func colorUV(v float64) string {
	switch {
	case v < 3:
		return ansiVerde
	case v < 6:
		return ansiAmarillo
	case v < 8:
		return ansiRojoVivo
	case v < 11:
		return ansiRojo
	}
	return ansiMagenta
}

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

func mostrarClima(w io.Writer, c *Clima, uv *UV, unidades string, color bool) {
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
	if uv != nil {
		txt := fmt.Sprintf("%.1f (%s)", uv.Actual, uv.Categoria)
		fmt.Fprintf(w, "  Índice UV:    %s · máx. hoy %.1f\n", pintar(color, colorUV(uv.Actual), txt), uv.MaxHoy)
		fmt.Fprintf(w, "    Hoy: %s\n", recomendacionUV(uv.MaxHoy))
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
		uvTxt := ""
		if d.UVMax != nil {
			uvTxt = pintar(color, colorUV(*d.UVMax), fmt.Sprintf(" UV %2.0f", *d.UVMax)) + "  "
		}
		fmt.Fprintf(w, "  %s  %s / %s   %3.0f%%  %s%s\n",
			etiqueta, min, max, d.ProbLluvia, uvTxt, pintar(color, ansiGris, capitalizar(d.Descripcion)))
	}
	fmt.Fprintln(w, "-----------------------------")
}

// ---------- Gráficas ----------

// Series que se pueden graficar con -serie.
const (
	SerieTemperatura = "temperatura"
	SerieLluvia      = "lluvia"
	SerieHumedad     = "humedad"
	SerieViento      = "viento"
	SerieUV          = "uv"
)

var seriesDisponibles = []string{SerieTemperatura, SerieLluvia, SerieHumedad, SerieViento, SerieUV}

// parsearSeries convierte "lluvia, humedad" en []string{"lluvia", "humedad"}.
// Valida los nombres y elimina repetidos. Una entrada vacía devuelve nil.
func parsearSeries(entrada string) ([]string, error) {
	var series []string
	vistas := map[string]bool{}
	for _, parte := range strings.Split(entrada, ",") {
		nombre := strings.ToLower(strings.TrimSpace(parte))
		if nombre == "" || vistas[nombre] {
			continue
		}
		valida := false
		for _, v := range seriesDisponibles {
			if nombre == v {
				valida = true
				break
			}
		}
		if !valida {
			return nil, fmt.Errorf("serie desconocida %q (opciones: %s)", nombre, strings.Join(seriesDisponibles, ", "))
		}
		vistas[nombre] = true
		series = append(series, nombre)
	}
	return series, nil
}

// Serie son los datos y el estilo de una gráfica de barras.
type Serie struct {
	Titulo  string
	Valores []float64
	Horas   []string
	Color   func(float64) string // color ANSI de cada barra; puede ser nil
	Formato string               // formato de los valores bajo cada barra, ej. "%.0f"
	// Con Fija=true el eje va de Min a Max (útil en porcentajes y en escalas que empiezan en 0).
	// Con Fija=false va de la mínima a la máxima de los datos.
	Fija     bool
	Min, Max float64
}

func maxValor(valores []float64) float64 {
	m := math.Inf(-1)
	for _, v := range valores {
		m = math.Max(m, v)
	}
	return m
}

func colorFijo(c string) func(float64) string { return func(float64) string { return c } }

// construirSerie extrae del pronóstico la serie pedida.
func construirSerie(nombre string, puntos []PuntoHora, unidades string) (Serie, error) {
	s := Serie{Horas: make([]string, len(puntos)), Valores: make([]float64, len(puntos)), Formato: "%.0f"}
	for i, p := range puntos {
		s.Horas[i] = p.Hora
	}

	switch nombre {
	case SerieTemperatura:
		s.Titulo = "Temperatura (" + strings.TrimSpace(simboloTemp(unidades)) + ")"
		s.Color = func(v float64) string { return colorTemp(v, unidades) }
		for i, p := range puntos {
			s.Valores[i] = p.Temp
		}
	case SerieLluvia:
		s.Titulo = "Probabilidad de lluvia (%)"
		s.Color = colorFijo(ansiAzul)
		s.Fija, s.Min, s.Max = true, 0, 100
		for i, p := range puntos {
			s.Valores[i] = p.ProbLluvia
		}
	case SerieHumedad:
		s.Titulo = "Humedad (%)"
		s.Color = colorFijo(ansiCian)
		s.Fija, s.Min, s.Max = true, 0, 100
		for i, p := range puntos {
			s.Valores[i] = float64(p.Humedad)
		}
	case SerieViento:
		s.Titulo = "Viento (" + unidadViento(unidades) + ")"
		s.Formato = "%.1f"
		s.Color = colorFijo(ansiMagenta)
		for i, p := range puntos {
			s.Valores[i] = p.Viento
		}
		s.Fija, s.Min, s.Max = true, 0, math.Max(maxValor(s.Valores), 1)
	case SerieUV:
		hay := false
		for i, p := range puntos {
			if p.UV != nil {
				hay = true
				s.Valores[i] = *p.UV
			}
		}
		if !hay {
			return Serie{}, errors.New("sin datos de UV (el servicio no respondió para esta ciudad)")
		}
		s.Titulo = "Índice UV"
		s.Formato = "%.1f"
		s.Color = colorUV
		s.Fija, s.Min, s.Max = true, 0, math.Max(maxValor(s.Valores), 3)
	default:
		return Serie{}, fmt.Errorf("serie desconocida %q", nombre)
	}
	return s, nil
}

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

// graficaBarras dibuja barras verticales con bloques Unicode.
// Cada fila tiene resolución de 1/8 de carácter, así que alto=6 da 48 niveles.
func graficaBarras(s Serie, color bool, alto int) string {
	n := len(s.Valores)
	if n == 0 || alto < 1 {
		return ""
	}

	min, max := s.Min, s.Max
	if !s.Fija {
		min, max = s.Valores[0], s.Valores[0]
		for _, v := range s.Valores {
			min, max = math.Min(min, v), math.Max(max, v)
		}
	} else if max <= min {
		max = min + 1
	}

	total := alto * 8
	niveles := make([]int, n)
	for i, v := range s.Valores {
		switch {
		case s.Fija:
			// Escala con cero real: un valor en el mínimo no dibuja barra.
			niv := int(math.Round((v - min) / (max - min) * float64(total)))
			niveles[i] = int(math.Max(0, math.Min(float64(total), float64(niv))))
		case max == min:
			niveles[i] = total / 2
		default:
			// Escala automática: la barra más baja siempre mide al menos 1/8.
			niveles[i] = 1 + int(math.Round((v-min)/(max-min)*float64(total-1)))
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
		for i, nivel := range niveles {
			celda := ' '
			switch {
			case nivel >= base+8:
				celda = '█'
			case nivel > base:
				celda = bloques[nivel-base-1]
			}
			barra := strings.Repeat(string(celda), anchoBarra)
			if celda != ' ' && s.Color != nil {
				barra = pintar(color, s.Color(s.Valores[i]), barra)
			}
			b.WriteString(" " + barra + " ")
		}
		b.WriteByte('\n')
	}

	// Eje horizontal, valores y horas.
	b.WriteString("       └" + strings.Repeat("─", anchoColumna*n) + "\n")
	b.WriteString(strings.Repeat(" ", margenIzq))
	for _, v := range s.Valores {
		b.WriteString(centrar(fmt.Sprintf(s.Formato, v), anchoColumna))
	}
	b.WriteByte('\n')
	b.WriteString(strings.Repeat(" ", margenIzq))
	for _, h := range s.Horas {
		etiqueta := h
		if t, err := time.Parse("2006-01-02T15:04", h); err == nil {
			etiqueta = t.Format("15") + "h"
		}
		b.WriteString(centrar(etiqueta, anchoColumna))
	}
	b.WriteByte('\n')
	return b.String()
}

// graficaTemperatura es un atajo para la serie de temperatura.
func graficaTemperatura(puntos []PuntoHora, unidades string, color bool, alto int) string {
	s, err := construirSerie(SerieTemperatura, puntos, unidades)
	if err != nil {
		return ""
	}
	return graficaBarras(s, color, alto)
}

// mostrarGrafica imprime una gráfica por cada serie pedida.
func mostrarGrafica(w io.Writer, puntos []PuntoHora, series []string, unidades string, color bool) {
	if len(puntos) == 0 {
		return
	}
	for i, nombre := range series {
		s, err := construirSerie(nombre, puntos, unidades)
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", capitalizar(nombre), err)
		} else {
			fmt.Fprintf(w, "%s, próximas 24 horas:\n", s.Titulo)
			fmt.Fprint(w, graficaBarras(s, color, 6))
		}
		if i < len(series)-1 {
			fmt.Fprintln(w)
		}
	}
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