# AGENTS.md

Guía para trabajar en este repositorio (personas y agentes). Léela antes de
tocar la carga de datos.

## Arranque rápido

```bash
make test            # go test ./...
make lint            # gofmt + go vet + tests
make build           # binario ./turislab (incluye la UI)
make run             # escáner + API + UI en :8090
make clean           # borra binario, UI compilada y turislab.db
```

La configuración local es `app/config.turislab.yaml` (no versionada; parte de
`config.turislab.example.yaml`). Las rutas relativas se resuelven contra el
directorio del fichero de configuración, **no** contra el directorio desde el
que se lanza el binario.

## Datasets: el contrato

**Todos los establecimientos que analiza la plataforma salen de
`app/datasets/*.csv`.** No hay otra fuente de entrada en el arranque normal.

- Al arrancar, el seeder lista **todos** los `*.csv` del directorio
  (`input.datasets_dir`, por defecto `./datasets` → `app/datasets/`), en orden
  alfabético, y hace *upsert* por `id`.
- Si dos ficheros repiten un `id`, gana el último por orden de nombre.
- Un fichero nuevo se carga solo con dejarlo ahí; no hay que tocar código.
- `input.path` y `--input <fichero>` son un modo alternativo de un solo
  fichero JSON/NDJSON. No se usan con el flujo normal.

### Formato

- UTF-8, **una fila de cabecera** y una fila por establecimiento.
- Delimitador `,` o `;` (se detecta en la línea de cabecera). BOM tolerado.
- Se permite **preámbulo** antes de la cabecera: se busca la primera línea que
  tenga una columna de nombre (`name`, `nombre`, `denominacion`…) y se descarta
  lo anterior. Los exports oficiales traen 5 líneas de título y fecha.
- `name` es la **única columna obligatoria**. Si falta `id`, se genera uno
  estable `gen-…` a partir de nombre + dirección + municipio.
- Los campos con el delimitador o saltos de línea van entre comillas.

### Columnas canónicas

| Columna | Tipo | Notas |
|---|---|---|
| `id` | texto | Identificador estable. Si falta, se genera |
| `name` | texto | **Obligatoria**; una fila sin nombre se omite (o falla en strict) |
| `category` | texto | `restaurant`, `hotel`, `bar`, `other` |
| `stars` | entero | `4`, o `4 ESTRELAS` en columnas de clasificación |
| `address`, `municipality`, `province`, `postal_code`, `country` | texto | `country` en ISO 3166-1 alpha-2 |
| `lat`, `lng` | decimal | `43.005030`, o `43,00503` si el fichero es `;` |
| `phone`, `email`, `website` | texto | |
| `registry_id` | texto | Número de registro oficial |
| `certifications` | lista | Separadas por `\|`, o por `,` si el fichero es `;` |
| `last_inspection_at` | fecha | RFC 3339, `YYYY-MM-DD` o `DD/MM/YYYY` |
| `last_inspection_result` | texto | |
| `google_place_id`, `tripadvisor_id`, `yelp_id`, `osm_id` | texto | IDs externos |
| `notes` | texto | Ver «Reglas»: nunca verdad terreno |
| `id_generated` | booleano | `true` / `false` |

Alias aceptados (entre otros): `sinatura`→`id`, `denominacion`→`name`,
`concello`→`municipality`, `enderezo`→`address`, `lonxitude`→`lng`,
`tipoloxia`/`tipo`→`category`, `mail`→`email`. Las columnas desconocidas se
ignoran, así que un export oficial se puede soltar tal cual.

### Reglas de mapeo que ya están implementadas

Están en `app/internal/turislab/seed/csv.go`; respétalas si añades un dataset.

1. **`categoria` es ambigua.** Si el fichero trae `tipo` **y** `categoria`
   (caso del directorio de aloxamentos del REAT), `tipo` es la categoría y
   `categoria` la clasificación por estrellas (`"4 ESTRELAS"`). Si solo hay
   `categoria`, es la categoría. Una columna `category` explícita gana siempre.
2. **Estrellas.** Solo se aceptan `N` o `N ESTRELA(S)/ESTRELLA(S)`. Valores
   como `SEGUNDA CATEGORÍA`, `1 CHAVE` o `GRUPO-B` dejan `stars` vacío (una
   llave no es una estrella).
3. **Categoría a partir de la etiqueta.** `Bares`/`Cafeterias`→`bar`,
   `Restaurantes`→`restaurant`, `HOTEIS`/`PENSIÓNS`/`HOSTAL`→`hotel`; el resto
   (albergues, apartamentos, turismo rural, campamentos, VUT…)→`other`.
4. **Coordenadas `0` o vacías → nulas.** Un `0,0` es «sin coordenadas», no un
   punto en el Atlántico. `osm` y `google_maps_scraper` dependen de `lat`/`lng`
   para funcionar bien.
5. **`codigo_postal: 0` → vacío.** El registro usa `0` como «desconocido».
6. **`correo_promocional` se ignora siempre.** Está en la lista
   `ignoredColumns`: es un contacto promocional y Turismo de Galicia prohíbe su
   uso comercial. No debe acabar en `email`.
7. **Municipio y provincia se normalizan** (`A CORUÑA` → `A Coruña`) para que
   los filtros cuadren entre directorios.

## Fuente oficial: REAT (Turismo de Galicia)

Directorio de empresas y actividades turísticas:
<https://aei.turismo.gal/es/rexistro-de-empresas-e-actividades-turisticas>

| Fichero | Filas | ¿Se usa? |
|---|---|---|
| `reat_directorio-restauracion_gal.csv` | 19.608 | **Sí** |
| `reat_directorio-aloxamentos_gal.csv` | 32.858 | **Sí**, excluyendo VUT |
| `reat_directorio-axencias_gal.csv` | 689 | No (coordenadas a 0) |
| `reat_directorio-guias_gal.csv` | — | No (son personas) |
| `REAT_Serie_Mensual.csv` | — | No (agregados, Latin-1) |

Notas de los ficheros:

- Fecha de referencia del export actual: **2026-11-01**.
- **Restauración no trae coordenadas, teléfono ni web.** Solo se puede
  rastrear por nombre + municipio en Google Maps.
- **Aloxamentos**: solo ~13 % tiene coordenadas, y las *viviendas de uso
  turístico* (28.334 de 32.858) **ninguna**. Por eso se excluyen del dataset.
- Los `id` (`R-LU-000710`, `H-LU-000469`…) son la *sinatura* oficial: úsalos
  como `id` y como `registry_id`.

### Regenerar el dataset

```bash
make datasets                                     # Lugo, 500 m alrededor de La Oca
make datasets REAT_FLAGS="-concello Vigo -max-alox 20 -streets COLON,URZAIZ"
```

El generador es `app/cmd/reat2csv`: descarga los directorios, filtra por
concello y radio, y escribe el CSV canónico. Flags:

| Flag | Defecto | Para qué |
|---|---|---|
| `-out` | `datasets/lugo.csv` | Fichero de salida (relativo a `app/`) |
| `-concello` | `Lugo` | Municipio a conservar |
| `-lat`/`-lng` | La Oca, Lugo | Centro del radio |
| `-radius` | `500` | Radio en metros para aloxamentos |
| `-streets` | `RAMON FERREIRO,NOREAS,CAMPO CASTELO` | Calles para restauración (no tiene coordenadas) |
| `-max-alox` / `-max-rest` | `6` / `4` | Tope por directorio |

Como restauración no tiene coordenadas, se selecciona por calle. Cuando dos
fichas comparten calle y número (mismo edificio) y una tiene coordenadas, el
generador **se las presta a la otra**: así La Oca hereda las de su dirección.
Es el único dato derivado; queda documentado aquí a propósito.

## Reglas al añadir o cambiar un dataset

- [ ] **Nada de verdad terreno en `notes`.** El juez LLM lee las notas: si
      escribes «cerró en 2025», el sistema acierta sin investigar y la demo
      deja de valer. El estado real debe descubrirlo el escáner.
- [ ] **`make clean` antes de arrancar** con un dataset nuevo. La carga es
      *upsert*: borrar un CSV no borra sus filas de la BD.
- [ ] **No inventes coordenadas ni webs.** Si no hay dato, se deja vacío y la
      fuente correspondiente se salta ese establecimiento.
- [ ] **No publiques `correo_promocional`** ni datos personales de guías.
- [ ] **Atribuye la fuente**: REAT — Axencia Turismo de Galicia, con la fecha
      de referencia del export.
- [ ] **Cuida el tamaño.** 10-50 fichas por CSV es cómodo para la PoC; un
      pase de escaneo a 1/min tarda ~13 h con 772 fichas y ~17 días con las
      ~24.000 de Galicia. Filtra por concello.
- [ ] **Añade un test** en `app/internal/turislab/seed/seed_test.go` si el
      fichero trae columnas o convenciones nuevas.

## Verificar que un dataset carga

```bash
make clean
make build
./turislab --config app/config.turislab.yaml --establishment no-existe
```

Debe imprimir `datasets dir: … (N csv files)` y una línea por fichero con
`cargados/total`. Si hay omisiones, el log dice fichero y número de registro.
