package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const climaJSON = `{"name":"%s","timezone":3600,"coord":{"lat":40.42,"lon":-3.7},
"main":{"temp":21.5,"feels_like":20.9,"temp_min":18,"temp_max":24,"pressure":1015,"humidity":55},
"weather":[{"description":"cielo claro"}],"wind":{"speed":3.2},
"sys":{"country":"ES","sunrise":1700000000,"sunset":1700040000}}`

const pronosticoJSON = `{"city":{"timezone":0},"list":[
{"dt":1700000000,"main":{"temp":10,"humidity":70},"weather":[{"description":"nubes"}],"wind":{"speed":2.5},"pop":0.2},
{"dt":1700010800,"main":{"temp":12,"humidity":85},"weather":[{"description":"lluvia"}],"wind":{"speed":4},"pop":0.8},
{"dt":1700021600,"main":{"temp":8,"humidity":90},"weather":[{"description":"lluvia"}],"wind":{"speed":6.5},"pop":0.1},
{"dt":1700032400,"main":{"temp":15,"humidity":60},"weather":[{"description":"nubes"}],"wind":{"speed":1},"pop":0}
]}`

// Respuesta de Open-Meteo alineada con las horas de pronosticoJSON.
const uvJSON = `{"current":{"time":"2023-11-14T22:00","interval":900,"uv_index":0.4},
"hourly":{"time":["2023-11-14T22:00","2023-11-15T01:00","2023-11-15T04:00","2023-11-15T07:00"],"uv_index":[0,0,0,1.5]},
"daily":{"time":["2023-11-14","2023-11-15"],"uv_index_max":[3.2,6.1]}}`

// servidorFalso simula la API de OpenWeatherMap.
func servidorFalso(t *testing.T, peticiones *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	comun := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		peticiones.Add(1)
		if r.URL.Query().Get("appid") != "clave-ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return "", false
		}
		q := r.URL.Query().Get("q")
		switch strings.ToLower(q) {
		case "limite":
			w.WriteHeader(http.StatusTooManyRequests)
			return "", false
		case "madrid", "lima", "bogota":
			return q, true
		}
		w.WriteHeader(http.StatusNotFound)
		return "", false
	}
	mux.HandleFunc("/weather", func(w http.ResponseWriter, r *http.Request) {
		if q, ok := comun(w, r); ok {
			fmt.Fprintf(w, climaJSON, q)
		}
	})
	mux.HandleFunc("/forecast", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := comun(w, r); ok {
			fmt.Fprint(w, pronosticoJSON)
		}
	})
	mux.HandleFunc("/uv/forecast", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("latitude") != "40.42" || q.Get("longitude") != "-3.70" || q.Get("timezone") != "auto" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, uvJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func clienteDePrueba(srv *httptest.Server, cache *Cache) *Cliente {
	return &Cliente{BaseURL: srv.URL, BaseUV: srv.URL + "/uv", APIKey: "clave-ok", Unidades: "metric", Idioma: "es", HTTP: srv.Client(), Cache: cache}
}

func TestClimaActual(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)

	c, err := cli.ClimaActual(context.Background(), "Madrid")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Madrid" || c.Main.Temp != 21.5 || c.Main.Humidity != 55 || c.Descripcion() != "cielo claro" {
		t.Errorf("datos inesperados: %+v", c)
	}
}

func TestErroresHTTP(t *testing.T) {
	var n atomic.Int32
	srv := servidorFalso(t, &n)

	casos := []struct {
		nombre, ciudad, clave string
		esperado              error
	}{
		{"404", "Nadaville", "clave-ok", ErrCiudadNoEncontrada},
		{"401", "Madrid", "mala", ErrAPIKeyInvalida},
		{"429", "limite", "clave-ok", ErrLimiteExcedido},
	}
	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			cli := clienteDePrueba(srv, nil)
			cli.APIKey = tc.clave
			_, err := cli.ClimaActual(context.Background(), tc.ciudad)
			if !errors.Is(err, tc.esperado) {
				t.Errorf("esperaba %v, obtuve %v", tc.esperado, err)
			}
		})
	}
}

func TestErrorDeConexionNoFiltraAPIKey(t *testing.T) {
	cli := &Cliente{BaseURL: "http://127.0.0.1:1", APIKey: "secreta123", Unidades: "metric", Idioma: "es",
		HTTP: &http.Client{Timeout: time.Second}}
	_, err := cli.ClimaActual(context.Background(), "Madrid")
	if err == nil {
		t.Fatal("esperaba un error de conexión")
	}
	if strings.Contains(err.Error(), "secreta123") {
		t.Errorf("el error filtra la API key: %v", err)
	}
}

func TestCache(t *testing.T) {
	var n atomic.Int32
	srv := servidorFalso(t, &n)
	cache, err := NuevoCacheEn(t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cli := clienteDePrueba(srv, cache)

	for i := 0; i < 3; i++ {
		if _, err := cli.ClimaActual(context.Background(), "Madrid"); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 1 {
		t.Errorf("la API se llamó %d veces, esperaba 1 (el resto debía salir de la caché)", n.Load())
	}

	// Caché expirada: vuelve a llamar a la API.
	cache.TTL = time.Nanosecond
	time.Sleep(5 * time.Millisecond)
	if _, err := cli.ClimaActual(context.Background(), "Madrid"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("tras expirar la caché esperaba 2 llamadas, hubo %d", n.Load())
	}
}

func TestAgruparPorDia(t *testing.T) {
	var r respuestaPronostico
	if err := json.Unmarshal([]byte(pronosticoJSON), &r); err != nil {
		t.Fatal(err)
	}
	dias := agruparPorDia(&r)
	if len(dias) != 2 {
		t.Fatalf("esperaba 2 días, obtuve %d: %+v", len(dias), dias)
	}
	d := dias[1]
	if d.Fecha != "2023-11-15" || d.TempMin != 8 || d.TempMax != 15 || d.ProbLluvia != 80 {
		t.Errorf("día 2 inesperado: %+v", d)
	}
	if d.Descripcion != "lluvia" && d.Descripcion != "nubes" {
		t.Errorf("descripción inesperada: %q", d.Descripcion)
	}
}

func TestConsultaConcurrente(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)

	ciudades := []string{"Madrid", "Nadaville", "Lima", "Bogota"}
	res := consultarCiudades(context.Background(), cli, ciudades, Opciones{Pronostico: true})

	if len(res) != len(ciudades) {
		t.Fatalf("esperaba %d resultados, obtuve %d", len(ciudades), len(res))
	}
	for i, r := range res {
		if r.Ciudad != ciudades[i] {
			t.Errorf("orden incorrecto en %d: %s", i, r.Ciudad)
		}
	}
	if !errors.Is(res[1].err, ErrCiudadNoEncontrada) {
		t.Errorf("Nadaville debía fallar con ErrCiudadNoEncontrada, obtuve %v", res[1].err)
	}
	for _, i := range []int{0, 2, 3} {
		if res[i].err != nil || res[i].Clima == nil || len(res[i].Pronostico) == 0 || len(res[i].Horas) == 0 {
			t.Errorf("%s debía tener clima y pronóstico: %+v", res[i].Ciudad, res[i])
		}
	}
}

func TestExtraerHoras(t *testing.T) {
	var r respuestaPronostico
	if err := json.Unmarshal([]byte(pronosticoJSON), &r); err != nil {
		t.Fatal(err)
	}
	h := extraerHoras(&r, 3)
	if len(h) != 3 || h[0].Hora != "2023-11-14T22:13" || h[0].Temp != 10 || h[2].Temp != 8 {
		t.Errorf("horas inesperadas: %+v", h)
	}
	if len(extraerHoras(&r, 100)) != 4 {
		t.Error("debía devolver solo las 4 lecturas disponibles")
	}
}

func TestGraficaTemperatura(t *testing.T) {
	puntos := []PuntoHora{{Hora: "2023-11-14T21:00", Temp: 0}, {Hora: "2023-11-15T00:00", Temp: 10}}
	lineas := strings.Split(strings.TrimRight(graficaTemperatura(puntos, "metric", false, 2), "\n"), "\n")
	if len(lineas) != 5 { // 2 filas + eje + temperaturas + horas
		t.Fatalf("esperaba 5 líneas, obtuve %d:\n%s", len(lineas), strings.Join(lineas, "\n"))
	}
	if !strings.HasPrefix(lineas[0], "  10.0 │") || !strings.HasPrefix(lineas[1], "   0.0 │") {
		t.Errorf("etiquetas del eje incorrectas:\n%s", strings.Join(lineas, "\n"))
	}
	if !strings.Contains(lineas[0], "████") || strings.Contains(lineas[0], "▁") {
		t.Errorf("fila superior incorrecta: %q", lineas[0])
	}
	if !strings.Contains(lineas[1], "▁▁▁▁") || !strings.Contains(lineas[1], "████") {
		t.Errorf("fila inferior incorrecta: %q", lineas[1])
	}
	if !strings.Contains(lineas[4], "21h") || !strings.Contains(lineas[4], "00h") {
		t.Errorf("horas incorrectas: %q", lineas[4])
	}

	// Casos borde: sin datos y todas las temperaturas iguales (no debe dividir entre cero).
	if graficaTemperatura(nil, "metric", false, 6) != "" {
		t.Error("sin datos debía devolver cadena vacía")
	}
	plana := graficaTemperatura([]PuntoHora{{Hora: "2023-11-14T21:00", Temp: 5}, {Hora: "2023-11-15T00:00", Temp: 5}}, "metric", false, 4)
	if strings.Contains(plana, "NaN") || !strings.Contains(plana, "█") {
		t.Errorf("gráfica plana incorrecta:\n%s", plana)
	}

	// Con color activo se envuelve en códigos ANSI.
	if !strings.Contains(graficaTemperatura(puntos, "metric", true, 2), ansiAzul) {
		t.Error("esperaba códigos de color")
	}
}

func TestImprimirJSON(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)
	res := consultarCiudades(context.Background(), cli, []string{"Madrid", "Nadaville"}, Opciones{Pronostico: true})

	var buf strings.Builder
	if err := imprimirJSON(&buf, res, false, true); err != nil {
		t.Fatal(err)
	}
	var salida []map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &salida); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, buf.String())
	}
	if len(salida) != 2 {
		t.Fatalf("esperaba 2 elementos, obtuve %d", len(salida))
	}
	if _, ok := salida[0]["pronostico"]; ok {
		t.Error("no se pidió -pronostico: no debía aparecer 'pronostico'")
	}
	if _, ok := salida[0]["proximas_horas"]; !ok {
		t.Error("se pidió -grafica: debía aparecer 'proximas_horas'")
	}
	if salida[1]["error"] == nil || salida[1]["clima"] != nil {
		t.Errorf("la ciudad fallida debía traer 'error' y no 'clima': %v", salida[1])
	}
}

func TestDividirCiudades(t *testing.T) {
	casos := map[string][]string{
		"Madrid":                       {"Madrid"},
		"Buenos Aires":                 {"Buenos Aires"},
		" Madrid ; Lima,PE ;; Bogotá ": {"Madrid", "Lima,PE", "Bogotá"},
		"":                             nil,
		" ; ":                          nil,
	}
	for entrada, esperado := range casos {
		got := dividirCiudades(entrada)
		if fmt.Sprint(got) != fmt.Sprint(esperado) {
			t.Errorf("dividirCiudades(%q) = %v, esperaba %v", entrada, got, esperado)
		}
	}
}

func TestAPIWeb(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)
	h := nuevoRouter(cli)

	// Éxito con dos ciudades
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/clima?ciudad=Madrid&ciudad=Lima&pronostico=1&uv=1", nil))
	if rec.Code != 200 {
		t.Fatalf("código %d: %s", rec.Code, rec.Body)
	}
	var res []Resultado
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || len(res) != 2 || res[0].Clima == nil || res[0].UV == nil {
		t.Fatalf("respuesta inesperada: %v / %s", err, rec.Body)
	}

	// Errores de validación
	for _, url := range []string{"/api/clima", "/api/clima?ciudad=Madrid&unidades=raras", "/api/clima?ciudad=Madrid&idioma=../x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != 400 {
			t.Errorf("%s: esperaba 400, obtuve %d", url, rec.Code)
		}
	}

	// Página principal
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Clima</title>") {
		t.Errorf("la página principal no se sirvió bien (código %d)", rec.Code)
	}
}

func TestColoresYFormato(t *testing.T) {
	if colorTemp(5, "metric") != ansiAzul || colorTemp(20, "metric") != ansiVerde || colorTemp(30, "metric") != ansiRojo {
		t.Error("colores incorrectos en metric")
	}
	if colorTemp(95, "imperial") != ansiRojo { // 95°F = 35°C
		t.Error("conversión de °F incorrecta")
	}
	if horaLocal(1700000000, 3600) != "23:13" {
		t.Errorf("horaLocal = %s", horaLocal(1700000000, 3600))
	}
}

func TestExtraerHorasTodasLasSeries(t *testing.T) {
	var r respuestaPronostico
	if err := json.Unmarshal([]byte(pronosticoJSON), &r); err != nil {
		t.Fatal(err)
	}
	h := extraerHoras(&r, 4)
	if h[1].Humedad != 85 || h[1].Viento != 4 || h[1].ProbLluvia != 80 {
		t.Errorf("series mal extraídas: %+v", h[1])
	}
}

func TestCategoriaUV(t *testing.T) {
	casos := []struct {
		v    float64
		want string
	}{{0, "Bajo"}, {2.9, "Bajo"}, {3, "Moderado"}, {5.9, "Moderado"}, {6, "Alto"}, {7.9, "Alto"}, {8, "Muy alto"}, {10.9, "Muy alto"}, {11, "Extremo"}, {14, "Extremo"}}
	for _, c := range casos {
		if got := categoriaUV(c.v); got != c.want {
			t.Errorf("categoriaUV(%v) = %s, esperaba %s", c.v, got, c.want)
		}
	}
}

func TestUVIntegrado(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)
	r := consultarCiudades(context.Background(), cli, []string{"Madrid"}, Opciones{Pronostico: true, UV: true})[0]

	if r.err != nil || r.Aviso != "" {
		t.Fatalf("error o aviso inesperado: %v / %q", r.err, r.Aviso)
	}
	if r.UV == nil || r.UV.Actual != 0.4 || r.UV.Categoria != "Bajo" || r.UV.MaxHoy != 3.2 {
		t.Errorf("UV inesperado: %+v", r.UV)
	}
	// UV máximo por día, emparejado por fecha local.
	if len(r.Pronostico) != 2 || r.Pronostico[0].UVMax == nil || *r.Pronostico[0].UVMax != 3.2 ||
		r.Pronostico[1].UVMax == nil || *r.Pronostico[1].UVMax != 6.1 {
		t.Errorf("UV diario inesperado: %+v", r.Pronostico)
	}
	// UV por hora, emparejado por hora (22:13 -> 22:00).
	if r.Horas[0].UV == nil || *r.Horas[0].UV != 0 || r.Horas[3].UV == nil || *r.Horas[3].UV != 1.5 {
		t.Errorf("UV por hora inesperado: %+v", r.Horas)
	}
}

func TestUVNoSeConsultaSiNoSePide(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)
	r := consultarCiudades(context.Background(), cli, []string{"Madrid"}, Opciones{Pronostico: true})[0]
	if r.UV != nil || r.Horas[0].UV != nil {
		t.Error("sin UV pedido no debía haber datos de UV")
	}
}

func TestUVFallaSinTirarElResto(t *testing.T) {
	var n atomic.Int32
	cli := clienteDePrueba(servidorFalso(t, &n), nil)
	cli.BaseUV = "http://127.0.0.1:1" // nada escucha aquí

	r := consultarCiudades(context.Background(), cli, []string{"Madrid"}, Opciones{UV: true})[0]
	if r.err != nil || r.Clima == nil {
		t.Fatalf("el clima debía mostrarse aunque falle el UV: %v", r.err)
	}
	if r.UV != nil || !strings.Contains(r.Aviso, "UV") {
		t.Errorf("esperaba aviso de UV, obtuve UV=%v aviso=%q", r.UV, r.Aviso)
	}
}

func TestParsearSeries(t *testing.T) {
	if s, err := parsearSeries(""); err != nil || s != nil {
		t.Errorf("entrada vacía: %v, %v", s, err)
	}
	s, err := parsearSeries(" Lluvia, humedad ,lluvia,,UV")
	if err != nil || fmt.Sprint(s) != "[lluvia humedad uv]" {
		t.Errorf("parsearSeries = %v, %v", s, err)
	}
	if _, err := parsearSeries("lluvia,granizo"); err == nil || !strings.Contains(err.Error(), "granizo") {
		t.Errorf("esperaba error por serie desconocida, obtuve %v", err)
	}
}

func TestConstruirSerie(t *testing.T) {
	var r respuestaPronostico
	if err := json.Unmarshal([]byte(pronosticoJSON), &r); err != nil {
		t.Fatal(err)
	}
	puntos := extraerHoras(&r, 4)

	lluvia, err := construirSerie(SerieLluvia, puntos, "metric")
	if err != nil || !lluvia.Fija || lluvia.Min != 0 || lluvia.Max != 100 || lluvia.Valores[1] != 80 {
		t.Errorf("serie de lluvia incorrecta: %+v, %v", lluvia, err)
	}
	viento, _ := construirSerie(SerieViento, puntos, "imperial")
	if viento.Max != 6.5 || !strings.Contains(viento.Titulo, "mph") {
		t.Errorf("serie de viento incorrecta: %+v", viento)
	}
	if _, err := construirSerie(SerieUV, puntos, "metric"); err == nil {
		t.Error("sin datos de UV debía devolver error")
	}
	uv := 4.0
	puntos[2].UV = &uv
	serieUV, err := construirSerie(SerieUV, puntos, "metric")
	if err != nil || serieUV.Valores[2] != 4 || serieUV.Max != 4 {
		t.Errorf("serie UV incorrecta: %+v, %v", serieUV, err)
	}
}

func TestGraficaEscalaFija(t *testing.T) {
	s := Serie{
		Titulo: "Prueba", Valores: []float64{0, 50, 100},
		Horas:   []string{"2023-11-14T09:00", "2023-11-14T12:00", "2023-11-14T15:00"},
		Formato: "%.0f", Fija: true, Min: 0, Max: 100,
	}
	lineas := strings.Split(strings.TrimRight(graficaBarras(s, false, 2), "\n"), "\n")
	if !strings.HasPrefix(lineas[0], " 100.0 │") || !strings.HasPrefix(lineas[1], "   0.0 │") {
		t.Errorf("ejes incorrectos:\n%s", strings.Join(lineas, "\n"))
	}
	// 0 no dibuja barra, 50 llena la mitad inferior, 100 llena todo.
	if strings.Count(lineas[0], "████") != 1 || strings.Count(lineas[1], "████") != 2 {
		t.Errorf("alturas incorrectas:\n%s", strings.Join(lineas, "\n"))
	}
	if !strings.Contains(lineas[3], "0") || !strings.Contains(lineas[3], "100") {
		t.Errorf("fila de valores incorrecta: %q", lineas[3])
	}

	// Valor fuera de rango: se recorta, no desborda ni entra en pánico.
	s.Valores = []float64{-20, 500, 100}
	if g := graficaBarras(s, false, 2); g == "" {
		t.Error("la gráfica con valores fuera de rango no debía quedar vacía")
	}
}

func TestMostrarGraficaVariasSeries(t *testing.T) {
	var r respuestaPronostico
	if err := json.Unmarshal([]byte(pronosticoJSON), &r); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	mostrarGrafica(&buf, extraerHoras(&r, 4), []string{SerieLluvia, SerieHumedad, SerieUV}, "metric", false)
	salida := buf.String()
	for _, esperado := range []string{"Probabilidad de lluvia (%)", "Humedad (%)", "Uv: sin datos de UV"} {
		if !strings.Contains(salida, esperado) {
			t.Errorf("falta %q en la salida:\n%s", esperado, salida)
		}
	}
}

func TestMostrarClimaConUV(t *testing.T) {
	var c Clima
	c.Name = "Madrid"
	var buf strings.Builder
	mostrarClima(&buf, &c, &UV{Actual: 4.5, Categoria: "Moderado", MaxHoy: 6.2}, "metric", false)
	salida := buf.String()
	if !strings.Contains(salida, "Índice UV") || !strings.Contains(salida, "4.5 (Moderado)") || !strings.Contains(salida, "máx. hoy 6.2") {
		t.Errorf("línea de UV incorrecta:\n%s", salida)
	}
	if !strings.Contains(salida, "protección especial") && !strings.Contains(salida, "sombra") && !strings.Contains(salida, "evita") {
		t.Errorf("falta la recomendación:\n%s", salida)
	}

	buf.Reset()
	mostrarClima(&buf, &c, nil, "metric", false)
	if strings.Contains(buf.String(), "Índice UV") {
		t.Error("sin UV no debía imprimirse esa línea")
	}
}