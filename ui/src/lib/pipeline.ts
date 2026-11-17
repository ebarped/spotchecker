/**
 * Static, human-readable detail for every pipeline phase. The phase list
 * itself comes from GET /api/pipeline (so the backend stays the source of
 * truth); this module adds the deep-dive page for each phase: what the code
 * actually does today and which parts are still a TODO.
 */

export type PhaseStatus = 'done' | 'partial' | 'todo';

export interface PhaseFact {
	label: string;
	value: string;
}

export interface FlowStep {
	title: string;
	body: string;
	status?: PhaseStatus;
}

export interface PhaseTableRow {
	cells: string[];
	status?: PhaseStatus;
}

export interface PhaseTable {
	title: string;
	columns: string[];
	rows: PhaseTableRow[];
	/** When true, the last column renders a StatusChip pill for
	 * row.status instead of plain text; cells then omit the status text. */
	statusColumn?: boolean;
}

/** One emitted signal of a source card: kind, family and how it reads. */
export interface SourceSignal {
	kind: string;
	family: string;
	detail: string;
}

/** Deep-dive card for one evidence source: what it contributes, how it
 * works internally and which signals it emits. */
export interface SourceCard {
	name: string;
	status: PhaseStatus;
	/** One line: what this source brings to the table. */
	what: string;
	/** How it works inside, step by step. */
	how: string[];
	/** Signals it emits and how they translate into evidence. */
	signals: SourceSignal[];
	/** Relevant knobs and their defaults. */
	config: string;
}

export interface PhaseBarItem {
	label: string;
	/** 0–100, used as the bar width. */
	pct: number;
	note: string;
	status?: PhaseStatus;
}

export interface PhaseDetail {
	id: number;
	slug: string;
	name: string;
	artifact: string;
	status: PhaseStatus;
	tagline: string;
	summary: string;
	facts: PhaseFact[];
	flow: FlowStep[];
	implemented: string[];
	todos: string[];
	table?: PhaseTable;
	/** Per-source deep-dive cards (phase 2). */
	sources?: SourceCard[];
	bars?: { title: string; items: PhaseBarItem[] };
	note?: string;
}

export const PHASE_STATUS: Record<PhaseStatus, { label: string }> = {
	done: { label: 'Implementado' },
	partial: { label: 'Parcial' },
	todo: { label: 'Pendiente' }
};

export const PIPELINE_DETAILS: PhaseDetail[] = [
	{
		id: 1,
		slug: 'ingesta-normalizacion',
		name: 'Ingesta y normalización',
		artifact: 'Establishment',
		status: 'partial',
		tagline: 'Del export del registro a un establecimiento limpio y estable.',
		summary:
			'El seeder lee el export del registro turístico, valida cada registro y lo deja en SQLite con un identificador estable. La carga es idempotente: volver a importar el mismo fichero actualiza, no duplica.',
		facts: [
			{ label: 'Entrada', value: 'datasets/*.csv (todos al arrancar)' },
			{ label: 'Destino', value: 'SQLite — database.dsn' },
			{ label: 'Idempotencia', value: 'upsert por id' },
			{ label: 'Registros inválidos', value: 'se saltan (o fallan en strict)' }
		],
		flow: [
			{
				title: 'Export del registro',
				body: 'JSON o NDJSON con los establecimientos del registro turístico.',
				status: 'done'
			},
			{
				title: 'Normalización',
				body: 'Trim del nombre, país por defecto y categoría desconocida → other.',
				status: 'partial'
			},
			{
				title: 'ID determinista',
				body: 'Si falta el id: gen-<sha256(nombre|dirección|municipio)>.',
				status: 'done'
			},
			{
				title: 'Upsert en SQLite',
				body: 'Los registros existentes se actualizan; los nuevos se insertan.',
				status: 'done'
			}
		],
		table: {
			title: 'Normalización campo a campo',
			columns: ['Campo', 'Entrada', 'Salida'],
			rows: [
				{ cells: ['name', '"  Hotel Sol  "', '"Hotel Sol"'], status: 'done' },
				{ cells: ['country', '""', '"ES" (default_country)'], status: 'done' },
				{ cells: ['category', '"hostal"', '"other"'], status: 'done' },
				{ cells: ['id', '""', '"gen-4f2a…"'], status: 'done' },
				{ cells: ['address', '"Av.  del Mar, 12"', 'sin normalizar'], status: 'todo' },
				{ cells: ['name', '"Hostal Peña"', 'sin normalizar Unicode'], status: 'todo' },
				{ cells: ['duplicados', 'misma ficha en dos filas', 'sin resolver'], status: 'todo' }
			]
		},
		implemented: [
			'Lectura de JSON y NDJSON con validación por registro.',
			'Trim del nombre, país por defecto y categoría desconocida normalizada a other.',
			'ID determinista y estable para registros sin identificador.',
			'Upsert idempotente en SQLite con migraciones.',
			'Modo estricto que falla en el primer registro inválido.',
			'Persistencia de coordenadas, contacto y metadatos del registro.'
		],
		todos: [
			'Entrada CSV: README la menciona, el seeder solo implementa JSON/NDJSON.',
			'Normalización Unicode y de direcciones/teléfonos/web (identity.normalize_unicode).',
			'Resolución de identidad y deduplicación entre filas y fuentes (pesos y radio configurados, sin implementación).',
			'Fusión de fichas duplicadas conservando la mejor evidencia de cada una.',
			'Sincronización de identificadores externos (place_id de Google, ids de OSM/Yelp…).'
		],
		note:
			'La configuración de identity (umbrales, pesos, radio geográfico) ya existe y se valida, pero todavía no hay consumidor: es el hueco principal de esta fase.'
	},
	{
		id: 2,
		slug: 'recogida-senales',
		name: 'Recogida de señales',
		artifact: 'Signal',
		status: 'partial',
		tagline: 'Cada fuente cuenta lo que ve, con su procedencia y su confianza.',
		summary:
			'Un scheduler recorre la base de establecimientos y lanza en paralelo las fuentes habilitadas. Cada adaptador traduce la respuesta de su fuente a señales tipadas (estado, rating, tags…) que se guardan con fuente, tipo y fecha.',
		facts: [
			{ label: 'Fuentes activas por defecto', value: 'website · osm · google_maps_scraper' },
			{ label: 'Cadencia', value: 'scan.trigger_interval (60 s por defecto)' },
			{ label: 'Objetivos por tick', value: 'scan.targets_per_tick (1)' },
			{ label: 'Concurrencia', value: 'scan.max_concurrent_sources' }
		],
		flow: [
			{
				title: 'Scheduler',
				body: 'Cada tick toma los siguientes establecimientos desde el cursor persistido.',
				status: 'done'
			},
			{
				title: 'Fan-out por fuente',
				body: 'Las fuentes habilitadas se ejecutan en paralelo con un semáforo.',
				status: 'done'
			},
			{
				title: 'Adaptador',
				body: 'Cada fuente convierte su respuesta en señales con familia y peso.',
				status: 'done'
			},
			{
				title: 'Persistencia',
				body: 'Las señales se guardan con procedencia, TTL y dedupe key.',
				status: 'done'
			},
			{
				title: 'Puntuación',
				body: 'El escaneo termina calculando el score con la evidencia histórica.',
				status: 'done'
			}
		],
		table: {
			title: 'Matriz de fuentes',
			columns: ['Fuente', 'Qué hace', 'Cómo se chequea', 'Estado'],
			statusColumn: true,
			rows: [
				{
					cells: [
						'website',
						'GET a la web del local: estado HTTP y keywords de cierre',
						'Señales web_status / web_keyword'
					],
					status: 'done'
				},
				{
					cells: [
						'osm',
						'Overpass a 100 m del POI: disused, closed, vacant, opening_hours',
						'Señal osm_tag por cada tag relevante'
					],
					status: 'done'
				},
				{
					cells: [
						'google_maps_scraper',
						'Binario gosom externo: ficha de Google Maps del local',
						'Señales business_status / rating / review_count'
					],
					status: 'done'
				},
				{
					cells: [
						'google_places',
						'API oficial de Google: businessStatus, rating y reseñas',
						'Sin adaptador todavía'
					],
					status: 'todo'
				},
				{
					cells: [
						'overture',
						'Dataset abierto (Parquet local): POI y estado',
						'Se ignora en silencio al construir las fuentes'
					],
					status: 'todo'
				},
				{
					cells: [
						'phone',
						'Llamada de verificación manual o por proveedor (Vapi, Retell, Dograh)',
						'Sin adaptador todavía'
					],
					status: 'todo'
				},
				{
					cells: [
						'registry',
						'Registro oficial por API o convenio',
						'Sin adaptador todavía'
					],
					status: 'todo'
				},
				{
					cells: [
						'news',
						'RSS, GNews o NewsAPI: noticias de cierres y traspasos',
						'Sin adaptador todavía'
					],
					status: 'todo'
				},
				{
					cells: [
						'yelp',
						'API de Yelp: ficha y reseñas',
						'Sin adaptador todavía'
					],
					status: 'todo'
				},
				{
					cells: [
						'tripadvisor',
						'Scraping Terra / Discover',
						'Sin adaptador todavía'
					],
					status: 'todo'
				}
			]
		},
		sources: [
			{
				name: 'website',
				status: 'done',
				what: 'Comprueba que la web del establecimiento responde y busca palabras que delatan un cierre.',
				how: [
					'Un único GET a la URL guardada en el registro, con User-Agent propio y timeout configurable (15 s por defecto).',
					'La petición sigue como máximo 3 redirects; el body se lee con un tope de 512 KB.',
					'La respuesta se clasifica: ok (2xx), 404, down (conexión rechazada, DNS caído o timeout) o error (cualquier otro código).',
					'Si la página carga, se busca en el HTML en minúsculas cada keyword configurada ("cerrado", "reformas", "traspaso"…).',
					'Sin URL en el registro no se emite nada: no es un error de la fuente.'
				],
				signals: [
					{
						kind: 'web_status',
						family: 'actividad',
						detail: 'Valor 1 si la web responde (confianza 0,75, peso 0,30). Una web caída no es un cierre: es un síntoma débil.'
					},
					{
						kind: 'web_keyword',
						family: 'estado directo',
						detail: 'Una keyword de cierre aparece en la página: valor 0,5, confianza 0,65, peso 0,50. Es la señal más fuerte de esta fuente.'
					}
				],
				config: 'timeout_seconds 15 · follow_redirects (máx. 3) · keywords [cerrado, reformas, traspaso…] · cache_ttl_days 7'
			},
			{
				name: 'osm',
				status: 'done',
				what: 'Consulta OpenStreetMap vía Overpass para ver si la comunidad ha marcado el POI como abandonado o cerrado.',
				how: [
					'POST a Overpass con una query around:100 sobre las coordenadas del establecimiento, filtrando nodos con tag amenity o tourism.',
					'El body se lee con un tope de 4 MB y timeout de 30 s; sin coordenadas, la fuente no hace nada.',
					'Se recorren los tags de cada elemento: disused:amenity, disused:tourism, closed=yes y shop=vacant.',
					'Un nodo con opening_hours cuyo nombre contiene el del establecimiento cuenta como actividad confirmada.',
					'Si no hay ningún elemento cerca, se emite una señal débil not_found en vez de nada.'
				],
				signals: [
					{
						kind: 'osm_tag: disused / closed / vacant',
						family: 'estado directo',
						detail: 'Valor 0,5–0,6 con confianza 0,55 y peso 0,60: la comunidad suele marcar los cierres, pero el dato envejece.'
					},
					{
						kind: 'osm_tag: found_active',
						family: 'actividad',
						detail: 'El POI existe y tiene horario: valor 1,0 con confianza 0,55 y peso 0,30.'
					},
					{
						kind: 'osm_tag: not_found',
						family: 'actividad',
						detail: 'Nada alrededor del radio: valor 0,0 con confianza 0,40 y peso 0,20.'
					}
				],
				config: 'overpass_url (oficial de Overpass) · timeout_seconds 30 · max_elements 5 · cache_ttl_days 30'
			},
			{
				name: 'google_maps_scraper',
				status: 'done',
				what: 'Lanza el binario gosom para leer la ficha de Google Maps del local tal y como la ve un usuario.',
				how: [
					'Construye la query "nombre + municipio" (sin dirección: confunde la búsqueda) y la escribe en un fichero temporal de entrada.',
					'Ejecuta el binario externo con -geo lat,lng para sesgar la búsqueda hacia el establecimiento, -lang es y -exit-on-inactivity para que un bloqueo no cuelgue el scan.',
					'Lee el JSON de salida (NDJSON, array u objeto suelto), recorta a max_places resultados y respeta el timeout de 120 s.',
					'Elige el mejor candidato por similitud de nombre normalizado (match exacto 100, contención 80, palabras compartidas): para un local cerrado Google puede responder solo con vecinos, así que el nombre es el discriminador fiable.',
					'Clasifica el estado del local: "CERRADO" / "cerrado permanentemente" → cierre permanente; "cerrado temporalmente" → temporal; el resto ("Closed ⋅ Opens 13:00", taglines) → abierto. Nunca confunde "cerrado por horario" con cierre.'
				],
				signals: [
					{
						kind: 'business_status',
						family: 'estado directo',
						detail: 'permanently_closed: valor 1,0, confianza 0,75, peso 0,90 — la señal más fuerte del sistema. temporarily_closed: 0,4. open: 0,0.'
					},
					{
						kind: 'rating',
						family: 'reseñas',
						detail: 'Nota media de Google en el momento del scrape, confianza 0,70 y peso 0,30.'
					},
					{
						kind: 'review_count',
						family: 'reseñas',
						detail: 'Número de reseñas acumuladas, confianza 0,70 y peso 0,20: una caída brusca es un síntoma.'
					}
				],
				config: 'provider gosom · binary (bin/google-maps-scraper) · max_places 3 · depth 1 · lang es · cache_ttl_days 7 · headless'
			}
		],
		implemented: [
			'Website: estado HTTP (ok, 404, down, error) y keywords de cierre en el HTML.',
			'OSM: tags disused, closed, vacant y opening_hours vía Overpass.',
			'Google Maps: cierre permanente/temporal, apertura, rating y reseñas con el scraper de gosom.',
			'Ejecución en paralelo de fuentes con límite de concurrencia.',
			'Fallos por fuente aislados: el escaneo sigue con el resto.',
			'Timeout y límite de tamaño de respuesta por fuente (512 KB web, 4 MB Overpass).'
		],
		todos: [
			'Adaptadores de google_places, overture, phone, registry, news, yelp y tripadvisor.',
			'Salud real por fuente en /api/report: ahora healthy se publica siempre como true.',
			'Jitter, backoff ante 429 y max_attempts: están en la config, pero el scanner no los aplica.',
			'Cumplir el TTL de caché por fuente (se guarda, todavía no se usa para reutilizar señales).',
			'Análisis de texto de reseñas (KindReviewText definido, sin adaptador que lo emita).',
			'Coste real del scan (scan_cost_eur siempre vale 0).'
		],
		note:
			'Overture aparece como enabled en el ejemplo, pero buildSources todavía no lo instancia: la fuente se ignora en silencio. Las fuentes pendientes se listan igualmente para que el mapa de lo que falta quede explícito.'
	},
	{
		id: 3,
		slug: 'procesamiento-evidencia',
		name: 'Procesamiento de evidencia',
		artifact: 'Evidence',
		status: 'partial',
		tagline: 'Una señal no es evidencia hasta que pasa el filtro de validez y el tiempo.',
		summary:
			'Las señales se almacenan con su procedencia y se leen para cada score. El motor descarta las que traen error, aplica un decaimiento temporal y detecta conflictos directos entre cierre y apertura. No hay todavía una capa de evidencia materializada.',
		facts: [
			{ label: 'Evidencia válida', value: 'señal con error == ""' },
			{ label: 'Vida media', value: '30 días (decay_half_life_days)' },
			{ label: 'Confianza agregada', value: 'media aritmética, suelo 0.1' },
			{ label: 'Conflictos', value: 'cierre directo vs apertura directa' }
		],
		flow: [
			{
				title: 'Señal bruta',
				body: 'Valor, familia, confianza, peso, timestamp y procedencia.',
				status: 'done'
			},
			{
				title: 'Filtro de validez',
				body: 'Las señales con error se descartan: no son evidencia.',
				status: 'done'
			},
			{
				title: 'Decaimiento',
				body: 'El peso efectivo se reduce a la mitad cada 30 días.',
				status: 'done'
			},
			{
				title: 'Agregación',
				body: 'Confianza media y detección de conflicto entre fuentes directas.',
				status: 'partial'
			},
			{
				title: 'Evidencia usable',
				body: 'Alimenta el motor de puntuación del establecimiento.',
				status: 'done'
			}
		],
		bars: {
			title: 'Peso efectivo según la antigüedad de la señal (vida media 30 días)',
			items: [
				{ label: '0 días', pct: 100, note: '100 %', status: 'done' },
				{ label: '30 días', pct: 50, note: '50 %', status: 'done' },
				{ label: '60 días', pct: 25, note: '25 %', status: 'done' },
				{ label: '90 días', pct: 12.5, note: '12,5 %', status: 'done' },
				{ label: '180 días', pct: 1.6, note: '≈1,6 %', status: 'done' }
			]
		},
		implemented: [
			'Procedencia completa: fuente, tipo, familia, fecha y payload.',
			'Exclusión de señales con error del cálculo de confianza y riesgo.',
			'Decaimiento exponencial con vida media configurable.',
			'Confianza del score como media de las señales válidas.',
			'Detección de conflicto cuando una fuente dice cerrado y otra abierto.',
			'Dedupe key por establecimiento, fuente, tipo y valor.'
		],
		todos: [
			'Caducidad efectiva por TTL: ttl_days se guarda pero no se purga ni se ignora al puntuar.',
			'Capa de evidencia materializada (hoy se recalculan las señales en cada score).',
			'Resolución de identidad entre fuentes para no sumar la misma evidencia dos veces.',
			'Análisis de reseñas y texto libre como familia de evidencia.',
			'Evidencia de calidad/impacto (rating, producto, prensa) más allá del estado.',
			'Pesos por familia calibrados en un único sitio, no hardcodeados en cada adaptador.'
		],
		note:
			'La fase existe de facto dentro de scoring: no hay paquete propio todavía. Lo que se ve aquí es el contrato de señal + el procesado que el motor aplica al leer.'
	},
	{
		id: 4,
		slug: 'motor-puntuacion',
		name: 'Motor de puntuación',
		artifact: 'Score',
		status: 'partial',
		tagline: 'Riesgo de cierre, prioridad y estado, con la confianza como freno.',
		summary:
			'El motor calcula el riesgo de cierre a partir de las señales de estado directo, lo combina con la antigüedad del último scan y lo corrige por confianza. La parte de calidad (riesgo y delta) está modelada y ponderada, pero todavía no se calcula.',
		facts: [
			{ label: 'Cierre confirmado', value: 'closure_risk ≥ 0,85' },
			{ label: 'Cierre sospechado', value: 'closure_risk ≥ 0,60' },
			{ label: 'Confianza', value: 'factor 0,7 + 0,3 × confianza' },
			{ label: 'Histéresis', value: 'cambio < 15 pts → mezcla 30 %' }
		],
		flow: [
			{
				title: 'Riesgo de cierre',
				body: 'Media ponderada de las señales de estado directo, con decaimiento.',
				status: 'done'
			},
			{
				title: 'Riesgo de calidad',
				body: 'Ratings, reseñas y señales de producto.',
				status: 'todo'
			},
			{
				title: 'Prioridad bruta',
				body: 'Suma ponderada de riesgos, delta de calidad y antigüedad.',
				status: 'partial'
			},
			{
				title: 'Ajuste por confianza',
				body: 'La evidencia escasa no puede disparar la prioridad.',
				status: 'done'
			},
			{
				title: 'Histéresis',
				body: 'Los cambios pequeños se suavizan contra el score anterior.',
				status: 'done'
			},
			{
				title: 'Estado y tier',
				body: 'Umbrales de cierre y asignación P0–P3 / collect_data.',
				status: 'done'
			}
		],
		bars: {
			title: 'Pesos de la prioridad (priority_weights)',
			items: [
				{ label: 'Riesgo de cierre', pct: 40, note: '40 % — activo', status: 'done' },
				{ label: 'Riesgo de calidad', pct: 25, note: '25 % — pendiente de calcular', status: 'todo' },
				{ label: 'Delta de calidad', pct: 15, note: '15 % — pendiente de calcular', status: 'todo' },
				{ label: 'Antigüedad', pct: 10, note: '10 % — activo', status: 'done' },
				{ label: 'Impacto', pct: 5, note: '5 % — configurado, sin uso', status: 'todo' },
				{ label: 'Quejas', pct: 5, note: '5 % — configurado, sin uso', status: 'todo' }
			]
		},
		table: {
			title: 'Umbrales de estado',
			columns: ['Condición', 'Estado sugerido'],
			rows: [
				{ cells: ['closure_risk ≥ 0,85', 'closure_confirmed'], status: 'done' },
				{ cells: ['closure_risk ≥ 0,60', 'closure_suspected'], status: 'done' },
				{ cells: ['closure_risk < 0,60', 'active'], status: 'done' },
				{ cells: ['cierre directo encontrado', 'suelo de riesgo 0,80'], status: 'done' },
				{ cells: ['apertura directa encontrada', 'techo de riesgo 0,35'], status: 'done' },
				{ cells: ['cierre y apertura a la vez', 'conflicto, techo 0,70'], status: 'done' }
			]
		},
		implemented: [
			'Riesgo de cierre a partir de señales de estado directo y business status.',
			'Suelos/techos cuando hay evidencia directa de cierre o apertura.',
			'Detección y marcado de conflicto entre fuentes.',
			'Prioridad con riesgo de cierre, antigüedad y factor de confianza.',
			'Histéresis contra el score anterior y marca de volatile.',
			'Asignación de estado y tier con umbrales configurables.'
		],
		todos: [
			'Calcular quality_risk y quality_delta con ratings, reseñas, producto y prensa.',
			'Aplicar los términos de impacto y quejas de la prioridad.',
			'Usar la última inspección y su resultado en la prioridad.',
			'Calibrar umbrales con backtesting sobre ground-truth.json.',
			'Documentar y validar los pesos por familia de evidencia.'
		],
		note:
			'Los pesos quality_risk, quality_delta, impact y complaints se leen de la config y entran en la fórmula, pero hoy los dos primeros valen 0 y los dos últimos ni siquiera se suman: la prioridad real se apoya en cierre + antigüedad. El estado sugerido que produce este motor es exactamente lo que el juez LLM (fase 5) recibe como referencia antes de firmar el veredicto final.'
	},
	{
		id: 5,
		slug: 'veredicto-juez',
		name: 'Veredicto del juez (LLM)',
		artifact: 'Verdict',
		status: 'done',
		tagline: 'Toda la evidencia sobre la mesa y un LLM que firma el estado final.',
		summary:
			'Después del motor determinista, un LLM recibe el expediente completo del establecimiento —datos del registro, cada señal con su fuente, antigüedad y confianza, y el veredicto heurístico del motor como referencia— y emite el estado final con una justificación legible. El motor decide quién va primero en la cola (prioridad y tier); el juez decide en qué estado está el local.',
		facts: [
			{ label: 'Modelo', value: 'judge.model — vacío reusa agent.model' },
			{ label: 'Temperatura', value: '0 — el juez quiere determinismo' },
			{ label: 'Límite por veredicto', value: '60 s (judge.timeout_seconds)' },
			{ label: 'Señales por expediente', value: '30, las más recientes (judge.max_signals)' }
		],
		flow: [
			{
				title: 'Veredicto heurístico',
				body: 'El motor determinista deja su estado sugerido, su motivo y el riesgo de cierre.',
				status: 'done'
			},
			{
				title: 'Expediente',
				body: 'Se arma un dossier JSON: registro, señales (fuente, tipo, valor, confianza, edad en días), cuántas fuentes fallaron y el veredicto del motor.',
				status: 'done'
			},
			{
				title: 'Juez LLM',
				body: 'El modelo recibe el expediente con un system prompt en español y responde JSON {verdict, confidence, rationale} a temperatura 0.',
				status: 'done'
			},
			{
				title: 'Veredicto final',
				body: 'La respuesta se valida (estados permitidos, confianza 0–1, justificación acotada) y sustituye al estado sugerido del motor.',
				status: 'done'
			},
			{
				title: 'Fallback seguro',
				body: 'Si el juez falla, se desactiva o responde basura, el veredicto del motor queda como final y el error se guarda.',
				status: 'done'
			}
		],
		table: {
			title: 'Vocabulario de veredictos',
			columns: ['Veredicto', 'Cuándo lo emite el juez'],
			rows: [
				{
					cells: ['active', 'La evidencia apunta a que el establecimiento funciona con normalidad.']
				},
				{
					cells: [
						'closure_suspected',
						'Hay señales de cierre (web caída, reseñas congeladas, tags) pero ninguna lo confirma.'
					]
				},
				{
					cells: [
						'closure_confirmed',
						'Una fuente directa (p. ej. Google Maps marcándolo) respalda el cierre permanente.'
					]
				},
				{ cells: ['closed_temporarily', 'Cerrado ahora pero con intención de volver: obras, vacaciones.'] },
				{ cells: ['seasonal', 'Cierre previsible por temporada, respaldado por el patrón de la evidencia.'] },
				{ cells: ['unknown', 'Evidencia insuficiente o contradictoria: el juez prefiere no adivinar.'] }
			]
		},
		implemented: [
			'Paquete judge: dossier de evidencia, system prompt en español y respuesta JSON estricta.',
			'Parseo tolerante: fences de markdown y prosa alrededor no rompen el veredicto.',
			'El veredicto del motor se conserva (heuristic_status, heuristic_reason) para comparar decisiones.',
			'Fallo aislado: el error del juez se guarda (judge_error) y el veredicto del motor se mantiene.',
			'Modelo compartido con el agente: judge.model vacío reusa agent.model y su API key.',
			'Solo se juzga con evidencia: sin señales, el veredicto del motor pasa directamente.'
		],
		todos: [
			'Calibrar el prompt contra ground-truth.json (acuerdo entre juez y realidad).',
			'Métricas de acuerdo y desacuerdo juez-motor en /metrics.',
			'Cachear veredictos para no re-juzgar expedientes que no han cambiado.',
			'Auto-consistencia: varios veredictos por expediente y votación.',
			'Exponer el coste acumulado del juez en el scan.'
		],
		note:
			'El juez decide solo el estado final; la prioridad y el tier siguen siendo del motor determinista, así que la posición en la cola nunca depende de una llamada al modelo. Un local sin ninguna señal ni score tampoco se juzga.'
	},
	{
		id: 6,
		slug: 'colas-tiers',
		name: 'Gestión de colas y tiers',
		artifact: 'QueueItem',
		status: 'done',
		tagline: 'La cola responde a una sola pregunta: ¿a quién visitar primero?',
		summary:
			'Cada establecimiento con score entra en la cola, se le asigna un tier y se ordena por prioridad descendente. El informe añade el resumen por tier, la cobertura y los casos de baja confianza que todavía no se pueden juzgar.',
		facts: [
			{ label: 'Orden', value: 'priority descendente' },
			{ label: 'P0', value: 'priority ≥ 45 y confianza ≥ 0,5' },
			{ label: 'P1 / P2', value: '≥ 35 / ≥ 20' },
			{ label: 'collect_data', value: 'confianza < 0,35' }
		],
		flow: [
			{
				title: 'Score listo',
				body: 'El motor deja prioridad, confianza, estado y evidencias.',
				status: 'done'
			},
			{
				title: 'Tier',
				body: 'La confianza manda: sin datos suficientes, collect_data.',
				status: 'done'
			},
			{
				title: 'Orden',
				body: 'La cola se ordena por prioridad y se numera el rank.',
				status: 'done'
			},
			{
				title: 'Resumen',
				body: 'Conteos por tier, cierres confirmados, baja confianza y cobertura.',
				status: 'done'
			},
			{
				title: 'Inspector',
				body: 'La cola solo ordena: la decisión de visita sigue siendo humana.',
				status: 'done'
			}
		],
		table: {
			title: 'Regla de asignación de tier',
			columns: ['Condición', 'Tier'],
			rows: [
				{ cells: ['confianza < 0,35', 'collect_data'], status: 'done' },
				{ cells: ['prioridad ≥ 45 y confianza ≥ 0,5', 'P0'], status: 'done' },
				{ cells: ['prioridad ≥ 45 y confianza < 0,5', 'P1'], status: 'done' },
				{ cells: ['prioridad 35–45', 'P1'], status: 'done' },
				{ cells: ['prioridad 20–35', 'P2'], status: 'done' },
				{ cells: ['prioridad < 20', 'P3'], status: 'done' }
			]
		},
		implemented: [
			'Asignación de tier con umbrales configurables.',
			'Orden estable por prioridad descendente y rank en la cola.',
			'Resumen por tier, cierres confirmados, baja confianza y cobertura.',
			'Cursor de escaneo para recorrer la base sin repetir siempre los mismos.',
			'Cola vacía hasta que existe al menos un score (no inventa datos).'
		],
		todos: [
			'Histéresis a nivel de tier para evitar saltos P0/P1 entre scans.',
			'Criterios de desempate cuando dos establecimientos tienen la misma prioridad.',
			'Asignación de inspectores y equilibrio de carga por municipio.',
			'Agrupar la cola en rutas de visita por proximidad geográfica.',
			'Coste estimado por establecimiento (scan_cost_eur).',
			'Mostrar el resultado de la última inspección en la cola.'
		],
		note:
			'El orden y los tiers están cerrados; lo que falta es la capa operativa: rutas, asignación y coste.'
	},
	{
		id: 7,
		slug: 'exposicion-supervision',
		name: 'Exposición y supervisión',
		artifact: 'Report',
		status: 'partial',
		tagline: 'El informe en vivo, la UI embebida y el asistente opcional.',
		summary:
			'Un bucle reconstruye el informe en memoria cada 10 segundos y el servidor chi lo sirve junto a la UI de SvelteKit embebida en el binario. La API cubre la cola, el resumen, la exportación, el estado del scan, las métricas y —si está habilitado— el agente.',
		facts: [
			{ label: 'Refresh del informe', value: 'cada 10 s (en memoria)' },
			{ label: 'UI', value: 'SvelteKit embebida (go:embed)' },
			{ label: 'API', value: 'chi: report, queue, summary, pipeline…' },
			{ label: 'Agente', value: '/api/agent/* si agent.enabled' }
		],
		flow: [
			{
				title: 'Store SQLite',
				body: 'Establecimientos, señales y scores persistidos.',
				status: 'done'
			},
			{
				title: 'Snapshot',
				body: 'refreshLoop reconstruye el informe completo cada 10 s.',
				status: 'done'
			},
			{
				title: 'API REST',
				body: 'Report, summary, queue, establishments, pipeline y health.',
				status: 'done'
			},
			{
				title: 'UI',
				body: 'Cola, KPIs, gráficos, cómo funciona y pestaña Agente.',
				status: 'done'
			},
			{
				title: 'Observabilidad',
				body: 'Métricas Prometheus y logs por paso.',
				status: 'partial'
			}
		],
		table: {
			title: 'Superficie expuesta',
			columns: ['Endpoint', 'Qué devuelve', 'Estado'],
			statusColumn: true,
			rows: [
				{ cells: ['GET /api/report', 'Informe completo'], status: 'done' },
				{ cells: ['GET /api/report/export', 'El mismo JSON como descarga'], status: 'done' },
				{ cells: ['GET /api/summary', 'Conteos por tier y estado'], status: 'done' },
				{ cells: ['GET /api/queue', 'Cola filtrable'], status: 'done' },
				{ cells: ['GET /api/establishments', 'Lista de establecimientos'], status: 'done' },
				{ cells: ['POST /api/establishments/{id}/refresh', 'Re-escaneo en segundo plano'], status: 'done' },
				{ cells: ['GET /api/scan/status', 'Estado del scanner'], status: 'done' },
				{ cells: ['GET /api/pipeline', 'Las fases del pipeline'], status: 'done' },
				{ cells: ['GET /metrics', 'Métricas Prometheus'], status: 'done' },
				{ cells: ['/api/agent/*', 'Info, catálogo y chat SSE'], status: 'done' },
				{ cells: ['Autenticación', 'Control de acceso a la API/UI'], status: 'todo' }
			]
		},
		implemented: [
			'UI SvelteKit embebida en el binario con fallback SPA.',
			'Informe en memoria refrescado cada 10 segundos.',
			'Todos los endpoints de consulta, exportación y re-escaneo.',
			'Estado del scan, salud, pipeline y métricas Prometheus.',
			'Agente montado solo cuando agent.enabled está activo.',
			'Página Cómo funciona con el detalle de cada fase.'
		],
		todos: [
			'Autenticación y autorización para API y UI.',
			'Actualización push (SSE/WebSocket) en vez de snapshot cada 10 s.',
			'Progreso detallado del scan y salud real por fuente.',
			'Coste del scan y trazabilidad de decisiones del inspector.',
			'Documento OpenAPI y límites de peticiones por endpoint.',
			'Métricas por fuente y por fase, no solo agregadas.'
		],
		note:
			'La exposición está resuelta para una PoC: no hay auth ni push, y la salud/coste son agregados. Es la fase con menos deuda funcional y más deuda operativa.'
	}
];

export function phaseDetail(slug: string): PhaseDetail | undefined {
	return PIPELINE_DETAILS.find((phase) => phase.slug === slug);
}

export function phaseByID(id: number): PhaseDetail | undefined {
	return PIPELINE_DETAILS.find((phase) => phase.id === id);
}
