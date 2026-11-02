#  clima

Herramienta de línea de comandos escrita en Go para consultar el **clima actual** y el **pronóstico** de una o varias ciudades usando la API de [OpenWeatherMap](https://openweathermap.org/api). Incluye gráfica de temperatura en la terminal, caché local, salida JSON y un pequeño servidor web.

Solo usa la biblioteca estándar de Go: **no tiene dependencias externas**.

## Características

- Clima actual: temperatura, sensación térmica, mínima y máxima, humedad, presión, viento, amanecer y atardecer (en hora local de la ciudad).
- Pronóstico de 5 días resumido por día, con probabilidad de lluvia.
- Gráficas de barras de las próximas 24 horas directamente en la terminal, con cinco series: temperatura, probabilidad de lluvia, humedad, viento e índice UV.
- Índice UV actual, con su categoría (escala de la OMS), máximo del día y recomendación, además del UV máximo de cada día del pronóstico.
- Varias ciudades consultadas **en paralelo** (goroutines, con un máximo de 5 simultáneas).
- Salida en **JSON** para usar con `jq` o desde otros programas.
- Caché local en archivos JSON (10 minutos por defecto) para no gastar cuota de la API.
- Configuración persistente: ciudad favorita, unidades e idioma.
- Servidor web opcional con API REST y una página HTML.
- Colores según la temperatura (azul, verde, rojo), desactivables.
- Errores claros según el código HTTP: ciudad no encontrada, API key inválida, límite de peticiones excedido.
- La API key nunca aparece en los mensajes de error.

## Requisitos

- [Go](https://go.dev/dl/) 1.22 o superior.
- Una API key gratuita de [OpenWeatherMap](https://home.openweathermap.org/api_keys). Las llaves nuevas pueden tardar un rato en activarse.

## Instalación

```bash
git clone https://github.com/TU_USUARIO/clima.git
cd clima
go build -o clima .
```

O directamente con `go install`:

```bash
go install github.com/TU_USUARIO/clima@latest
```

## Configurar la API key

La llave se lee de la variable de entorno `OPENWEATHER_API_KEY`. **Nunca la escribas en el código ni la subas al repositorio.**

**PowerShell (Windows):**

```powershell
$env:OPENWEATHER_API_KEY = "tu_llave"        # solo para esta sesión
setx OPENWEATHER_API_KEY "tu_llave"          # permanente (abre una terminal nueva)
```

**CMD (Windows):**

```cmd
set OPENWEATHER_API_KEY=tu_llave
```

**Linux / macOS:**

```bash
export OPENWEATHER_API_KEY=tu_llave
```

## Uso

```
clima [opciones] [ciudad[; ciudad2; ...]]
```

> Las opciones van **antes** de la ciudad. Las ciudades con espacios no necesitan comillas, y para varias ciudades se separan con `;` (así `Lima,PE` sigue funcionando con el código de país).

```bash
clima Madrid
clima Buenos Aires
clima -pronostico Madrid
clima -grafica Madrid
clima -serie lluvia,humedad,viento Madrid     # otras series (implica -grafica)
clima -uv -pronostico Madrid                  # índice UV actual y máximo por día
clima -serie temperatura,uv Madrid
clima -unidades imperial -idioma en Miami
clima -pronostico "Madrid; Lima; Bogotá"      # varias ciudades en paralelo
clima -json -pronostico Madrid | jq .
```

### Opciones

| Opción | Descripción | Por defecto |
|---|---|---|
| `-pronostico` | Muestra el pronóstico de 5 días | desactivado |
| `-grafica` | Muestra la gráfica de temperatura de las próximas 24 horas | desactivado |
| `-serie` | Series de la gráfica separadas por coma: `temperatura`, `lluvia`, `humedad`, `viento`, `uv` (implica `-grafica`) | `temperatura` |
| `-uv` | Muestra el índice UV (usa el servicio gratuito Open-Meteo) | desactivado |
| `-json` | Imprime el resultado como JSON | desactivado |
| `-unidades` | `metric` (°C), `imperial` (°F) o `standard` (K) | `metric` |
| `-idioma` | Idioma de las descripciones (`es`, `en`, `fr`, `pt_br`...) | `es` |
| `-sin-cache` | No usar la caché local | desactivado |
| `-cache-ttl` | Duración de la caché | `10m` |
| `-sin-color` | Desactivar colores | desactivado |
| `-timeout` | Timeout de cada petición HTTP | `10s` |
| `-servidor` | Inicia el servidor web en la dirección dada (ej. `:8080`) | |
| `-guardar-config` | Guarda ciudad, unidades e idioma como valores por defecto | |

Los colores se desactivan automáticamente si la salida no es una terminal o si existe la variable `NO_COLOR`.

### Gráficas

`-grafica` dibuja la temperatura; con `-serie` eliges qué graficar, y si pides varias se dibuja una gráfica por cada una:

```
Probabilidad de lluvia (%), próximas 24 horas:
 100.0 │                         ▁▁▁▁             
       │                         ████  ▂▂▂       
       │                  ▅▅▅  ████  ████       
       │             ▁▁▁▁  ████  ████  ████  ▃▃▃▃ 
       │              ████  ████  ████  ████  ████ 
   0.0 │ ▂▂▂▂    ▅▅▅  ████  ████  ████  ████  ████ 
       └────────────────────────────────────────────────
          5     0     10    35    60    85    70    40  
         09h   12h   15h   18h   21h   00h   03h   06h  
```

- La temperatura usa un eje automático (de la mínima a la máxima del período), así que la barra más baja nunca desaparece.
- Lluvia y humedad usan un eje fijo de 0 a 100 %; viento e índice UV empiezan en 0. En estas series una barra vacía significa realmente cero.
- Las horas son las de la ciudad consultada. El pronóstico llega en tramos de 3 horas.
- Las barras se colorean según la serie; el UV usa un color por categoría de la OMS.

### Índice UV

OpenWeatherMap retiró su API de UV en 2021 y hoy solo la ofrece en One Call 3.0, que exige suscripción con tarjeta de crédito. Por eso el UV se consulta a [Open-Meteo](https://open-meteo.com/), que es gratuito para uso no comercial y **no necesita API key**, usando las coordenadas que ya devuelve OpenWeatherMap.

```
  Índice UV:    5.8 (Moderado) · máx. hoy 6.2
    Hoy: protector, sombrero y gafas; evita el sol de 12 a 16 h.
```

Con `-pronostico` cada día muestra además su UV máximo. Si el servicio de UV no responde, el resto de los datos se muestra igualmente y se imprime un aviso. La categoría usa la escala de la OMS: Bajo (<3), Moderado (3–5), Alto (6–7), Muy alto (8–10) y Extremo (11+). Las recomendaciones son orientativas.

### Salida JSON

Con `-json` se imprime un arreglo con una entrada por ciudad. El campo `pronostico` solo aparece con `-pronostico`, y `proximas_horas` (temperatura, humedad, viento, probabilidad de lluvia y UV de cada tramo de 3 horas) con `-grafica` o `-serie`. Con `-uv` aparece el objeto `uv`; si ese servicio falla, se incluye `aviso` y el resto de los datos se conserva. Si una ciudad falla, su entrada incluye el campo `error` y el programa termina con código de salida 1.

```bash
clima -json Madrid | jq '.[0].clima.main.temp'
```

### Configuración guardada

```bash
clima -guardar-config -unidades metric -idioma es Madrid
clima        # ahora consulta Madrid sin indicar la ciudad
```

| Sistema | Configuración | Caché |
|---|---|---|
| Windows | `%AppData%\clima-cli\config.json` | `%LocalAppData%\clima-cli\` |
| Linux | `~/.config/clima-cli/config.json` | `~/.cache/clima-cli/` |
| macOS | `~/Library/Application Support/clima-cli/config.json` | `~/Library/Caches/clima-cli/` |

Orden de prioridad: valores por defecto → configuración guardada → opciones de la línea de comandos.

## Servidor web

```bash
clima -servidor :8080
```

Abre <http://localhost:8080> para usar la página, o consulta la API directamente:

```
GET /api/clima?ciudad=Madrid&ciudad=Lima&pronostico=1&unidades=metric&idioma=es
```

| Parámetro | Descripción |
|---|---|
| `ciudad` | Obligatorio. Se puede repetir (máximo 10) o separar con `;` codificado como `%3B` |
| `pronostico` | `1` para incluir pronóstico y próximas horas |
| `uv` | `1` para incluir el índice UV |
| `unidades` | `metric`, `imperial` o `standard` |
| `idioma` | Código de idioma, por ejemplo `es` |

>  El servidor no tiene autenticación y cada consulta gasta la cuota de **tu** API key. No lo expongas a internet sin protegerlo.

## Estructura del proyecto

| Archivo | Contenido |
|---|---|
| `main.go` | Flags, configuración, códigos de salida y arranque |
| `cliente.go` | Cliente de la API, modelos, pronóstico por día y consultas concurrentes |
| `presentacion.go` | Salida en texto, colores, series y gráficas de barras, y JSON |
| `uv.go` | Cliente del servicio de UV (Open-Meteo), categorías y recomendaciones |
| `cache.go` | Caché en archivos con escritura atómica |
| `config.go` | Configuración persistente |
| `servidor.go` | API REST y página web |
| `main_test.go` | Tests con `httptest` (sin llamar a la API real) |

## Tests

```bash
go test ./...
go test -race -cover ./...
```

Los tests usan un servidor HTTP simulado, así que no necesitan API key ni conexión a internet.

## Ideas para el futuro

- Dibujar las gráficas en la página web.
- Reintentos con backoff exponencial ante errores 429 y 5xx.
- Interfaz `ProveedorClima` para soportar otros servicios, como Open-Meteo.
- Modo `-cada 5m` que refresque la pantalla automáticamente.
- Calidad del aire y alertas meteorológicas.

## Créditos

- Datos meteorológicos: [OpenWeatherMap](https://openweathermap.org/).
- Datos de índice UV: [Open-Meteo.com](https://open-meteo.com/) (licencia CC BY 4.0).