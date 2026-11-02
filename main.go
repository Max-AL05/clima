// Comando clima: consulta el clima y el pronóstico usando OpenWeatherMap.
//
// Uso:
//
//	export OPENWEATHER_API_KEY=tu_llave
//	go run . Madrid
//	go run . Buenos Aires
//	go run . -pronostico "Madrid; Lima; Bogotá"   (las opciones van ANTES de la ciudad)
//	go run . -grafica Madrid                      (gráfica de temperatura, próximas 24 h)
//	go run . -serie lluvia,humedad,viento Madrid  (otras series; implica -grafica)
//	go run . -uv -pronostico Madrid               (índice UV actual y máximo por día)
//	go run . -json -pronostico Madrid | jq        (salida JSON para scripts)
//	go run . -servidor :8080
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

// dividirCiudades separa una lista de ciudades por ";" (así "Madrid,ES" sigue funcionando).
func dividirCiudades(entrada string) []string {
	var ciudades []string
	for _, c := range strings.Split(entrada, ";") {
		if c = strings.TrimSpace(c); c != "" {
			ciudades = append(ciudades, c)
		}
	}
	return ciudades
}

// esTerminal indica si stdout es una terminal (para decidir si usamos colores).
func esTerminal() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func main() {
	os.Exit(run())
}

// run devuelve el código de salida; así los defer se ejecutan antes de os.Exit.
func run() int {
	cfg := CargarConfig()

	unidades := flag.String("unidades", cfg.Unidades, "unidades: metric (°C), imperial (°F) o standard (K)")
	idioma := flag.String("idioma", cfg.Idioma, "idioma de las descripciones (es, en, fr, ...)")
	pronostico := flag.Bool("pronostico", false, "mostrar pronóstico de 5 días")
	sinCache := flag.Bool("sin-cache", false, "no usar la caché local")
	ttl := flag.Duration("cache-ttl", 10*time.Minute, "duración de la caché")
	grafica := flag.Bool("grafica", false, "mostrar gráfica de temperatura de las próximas 24 horas")
	serie := flag.String("serie", "", "series de la gráfica separadas por coma: temperatura, lluvia, humedad, viento, uv (implica -grafica)")
	uv := flag.Bool("uv", false, "mostrar el índice UV (servicio gratuito Open-Meteo)")
	salidaJSON := flag.Bool("json", false, "imprimir el resultado como JSON (sin colores ni texto decorativo)")
	sinColor := flag.Bool("sin-color", false, "desactivar colores")
	timeout := flag.Duration("timeout", 10*time.Second, "timeout de cada petición HTTP")
	servidor := flag.String("servidor", "", "iniciar servidor web en esta dirección (ej: :8080)")
	guardar := flag.Bool("guardar-config", false, "guardar ciudad (la primera), unidades e idioma como valores por defecto")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: %s [opciones] [ciudad[; ciudad2; ...]]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Requiere la variable de entorno OPENWEATHER_API_KEY.")
		fmt.Fprintln(os.Stderr, "Si no indicas ciudad se usa la favorita de la configuración.\n\nOpciones:")
		flag.PrintDefaults()
	}
	flag.Parse()

	if !unidadValida(*unidades) {
		fmt.Fprintln(os.Stderr, "Error: -unidades debe ser metric, imperial o standard")
		return 2
	}
	if !reIdioma.MatchString(*idioma) {
		fmt.Fprintln(os.Stderr, "Error: -idioma inválido (ejemplos: es, en, pt_br)")
		return 2
	}

	series, err := parsearSeries(*serie)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	conGrafica := *grafica || len(series) > 0
	if conGrafica && len(series) == 0 {
		series = []string{SerieTemperatura}
	}
	conUV := *uv || slices.Contains(series, SerieUV)

	// Las ciudades pueden venir sin comillas ("Buenos Aires"): unimos los argumentos con espacio.
	ciudades := dividirCiudades(strings.Join(flag.Args(), " "))
	if len(ciudades) == 0 && cfg.CiudadFavorita != "" {
		ciudades = []string{cfg.CiudadFavorita}
	}

	if *guardar {
		if len(ciudades) > 0 {
			cfg.CiudadFavorita = ciudades[0]
		}
		cfg.Unidades, cfg.Idioma = *unidades, *idioma
		ruta, err := cfg.Guardar()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println("Configuración guardada en", ruta)
		return 0
	}

	apiKey := os.Getenv("OPENWEATHER_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: define la variable de entorno OPENWEATHER_API_KEY")
		return 1
	}
	if len(ciudades) == 0 && *servidor == "" {
		flag.Usage()
		return 2
	}

	var cache *Cache
	if !*sinCache {
		c, err := NuevoCache(*ttl)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Aviso: no se pudo crear la caché:", err)
		} else {
			cache = c
		}
	}
	cli := NuevoCliente(apiKey, *unidades, *idioma, *timeout, cache)

	// Ctrl+C cancela las peticiones en curso y apaga el servidor ordenadamente.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *servidor != "" {
		fmt.Println("Servidor escuchando en", *servidor, "(Ctrl+C para salir)")
		if err := iniciarServidor(ctx, *servidor, cli); err != nil {
			fmt.Fprintln(os.Stderr, "Error del servidor:", err)
			return 1
		}
		return 0
	}

	color := !*sinColor && esTerminal() && os.Getenv("NO_COLOR") == ""
	// La gráfica necesita los datos del pronóstico, aunque no se muestre el resumen diario.
	resultados := consultarCiudades(ctx, cli, ciudades, Opciones{
		Pronostico: *pronostico || conGrafica,
		UV:         conUV,
	})

	fallos := 0
	for _, r := range resultados {
		if r.err != nil {
			fallos++
		}
	}

	if *salidaJSON {
		// Los errores van dentro del JSON (campo "error"); el código de salida sigue siendo 1.
		if err := imprimirJSON(os.Stdout, resultados, *pronostico, conGrafica); err != nil {
			fmt.Fprintln(os.Stderr, "Error al generar el JSON:", err)
			return 1
		}
	} else {
		for _, r := range resultados {
			if r.err != nil {
				fmt.Fprintf(os.Stderr, "Error (%s): %v\n", r.Ciudad, r.err)
				continue
			}
			if r.Aviso != "" {
				fmt.Fprintf(os.Stderr, "Aviso (%s): %s\n", r.Ciudad, r.Aviso)
			}
			mostrarClima(os.Stdout, r.Clima, r.UV, *unidades, color)
			if *pronostico {
				mostrarPronostico(os.Stdout, r.Pronostico, *unidades, color)
			}
			if conGrafica {
				mostrarGrafica(os.Stdout, r.Horas, series, *unidades, color)
			}
		}
	}
	if fallos > 0 {
		return 1
	}
	return 0
}