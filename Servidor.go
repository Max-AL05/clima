package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

const maxCiudadesWeb = 10

var reIdioma = regexp.MustCompile(`^[A-Za-z_]{2,5}$`)

func unidadValida(u string) bool {
	return u == "metric" || u == "imperial" || u == "standard"
}

func escribirJSON(w http.ResponseWriter, estado int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(estado)
	json.NewEncoder(w).Encode(v)
}

// manejadorAPI atiende GET /api/clima?ciudad=Madrid&ciudad=Lima&pronostico=1&uv=1&unidades=metric&idioma=es
func manejadorAPI(base *Cliente) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// Acepta ?ciudad=Madrid&ciudad=Lima o ?ciudad=Madrid%3BLima (";" codificado).
		var ciudades []string
		for _, valor := range q["ciudad"] {
			ciudades = append(ciudades, dividirCiudades(valor)...)
		}
		if len(ciudades) == 0 {
			escribirJSON(w, http.StatusBadRequest, map[string]string{"error": "falta el parámetro 'ciudad'"})
			return
		}
		if len(ciudades) > maxCiudadesWeb {
			escribirJSON(w, http.StatusBadRequest, map[string]string{"error": "demasiadas ciudades (máximo 10)"})
			return
		}

		cli := *base // copia superficial: comparte HTTP y Cache, pero podemos cambiar unidades/idioma
		if u := q.Get("unidades"); u != "" {
			if !unidadValida(u) {
				escribirJSON(w, http.StatusBadRequest, map[string]string{"error": "unidades debe ser metric, imperial o standard"})
				return
			}
			cli.Unidades = u
		}
		if l := q.Get("idioma"); l != "" {
			if !reIdioma.MatchString(l) {
				escribirJSON(w, http.StatusBadRequest, map[string]string{"error": "idioma inválido"})
				return
			}
			cli.Idioma = l
		}

		res := consultarCiudades(r.Context(), &cli, ciudades, Opciones{
			Pronostico: q.Get("pronostico") == "1",
			UV:         q.Get("uv") == "1",
		})
		escribirJSON(w, http.StatusOK, res)
	}
}

func manejadorPagina(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(paginaHTML))
}

func nuevoRouter(cli *Cliente) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", manejadorPagina)
	mux.HandleFunc("GET /api/clima", manejadorAPI(cli))
	return mux
}

// iniciarServidor bloquea hasta que ctx se cancele (Ctrl+C) o el servidor falle.
func iniciarServidor(ctx context.Context, addr string, cli *Cliente) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           nuevoRouter(cli),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		cierre, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(cierre); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

const paginaHTML = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Clima</title>
<style>
  :root { color-scheme: light dark; --borde: #8884; --acento: #2563eb; }
  body { font-family: system-ui, sans-serif; max-width: 760px; margin: 2rem auto; padding: 0 1rem; }
  form { display: flex; gap: .5rem; flex-wrap: wrap; align-items: center; margin-bottom: 1.5rem; }
  input[type=text] { flex: 1; min-width: 220px; padding: .5rem; }
  button { padding: .5rem 1rem; background: var(--acento); color: #fff; border: 0; border-radius: 6px; cursor: pointer; }
  .card { border: 1px solid var(--borde); border-radius: 10px; padding: 1rem; margin-bottom: 1rem; }
  .card h2 { margin: 0 0 .5rem; }
  .temp { font-size: 2rem; font-weight: 600; }
  .error { color: #dc2626; }
  .dia { display: flex; justify-content: space-between; padding: .25rem 0; border-top: 1px solid var(--borde); }
  small { opacity: .7; }
</style>
</head>
<body>
<h1>🌤️ Clima</h1>
<form id="f">
  <input type="text" id="ciudad" placeholder="Ciudades separadas por ; (ej: Madrid; Buenos Aires)" required>
  <label><input type="checkbox" id="pron"> Pronóstico</label>
  <select id="uni">
    <option value="metric">°C</option>
    <option value="imperial">°F</option>
    <option value="standard">K</option>
  </select>
  <button type="submit">Consultar</button>
</form>
<div id="out"></div>
<script>
var SIMBOLO = { metric: '°C', imperial: '°F', standard: ' K' };

function el(tag, texto, clase) {
  var e = document.createElement(tag);
  if (texto !== undefined) e.textContent = texto;
  if (clase) e.className = clase;
  return e;
}

function tarjeta(d) {
  var card = el('div', undefined, 'card');
  card.appendChild(el('h2', d.ciudad));
  if (d.error) { card.appendChild(el('p', d.error, 'error')); return card; }
  var s = SIMBOLO[d.unidades] || '';
  var c = d.clima;
  card.appendChild(el('div', c.main.temp.toFixed(1) + s, 'temp'));
  var desc = (c.weather && c.weather[0]) ? c.weather[0].description : '';
  card.appendChild(el('p', desc + ' · sensación ' + c.main.feels_like.toFixed(1) + s));
  card.appendChild(el('small', 'Humedad ' + c.main.humidity + '% · Presión ' + c.main.pressure + ' hPa · Viento ' + c.wind.speed + ' ' + (d.unidades === 'imperial' ? 'mph' : 'm/s')));
  (d.pronostico || []).forEach(function (p) {
    var fila = el('div', undefined, 'dia');
    fila.appendChild(el('span', p.fecha));
    fila.appendChild(el('span', p.temp_min.toFixed(1) + s + ' / ' + p.temp_max.toFixed(1) + s + ' · ☔ ' + Math.round(p.prob_lluvia) + '% · ' + p.descripcion));
    card.appendChild(fila);
  });
  return card;
}

document.getElementById('f').addEventListener('submit', async function (e) {
  e.preventDefault();
  var out = document.getElementById('out');
  out.textContent = 'Consultando…';
  var params = new URLSearchParams({
    ciudad: document.getElementById('ciudad').value,
    pronostico: document.getElementById('pron').checked ? '1' : '0',
    unidades: document.getElementById('uni').value
  });
  try {
    var r = await fetch('/api/clima?' + params);
    var data = await r.json();
    if (!r.ok) { out.textContent = data.error || 'Error'; return; }
    out.replaceChildren.apply(out, data.map(tarjeta));
  } catch (err) {
    out.textContent = 'Error de conexión';
  }
});
</script>
</body>
</html>`