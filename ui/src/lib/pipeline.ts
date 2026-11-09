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
			{ label: 'Entrada', value: 'base-de-datos.json (JSON o NDJSON)' },
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
			columns: ['Fuente', 'Qué consulta', 'Estado'],
			rows: [
				{
					cells: ['website', 'HTTP de la web; estado y keywords de cierre', 'Implementado'],
					status: 'done'
				},
				{
					cells: ['osm', 'Overpass a 100 m; disused, closed, vacant, opening_hours', 'Implementado'],
					status: 'done'
				},
				{
					cells: ['google_maps_scraper', 'gosom/kit; estado, rating y número de reseñas', 'Implementado'],
					status: 'done'
				},
				{
					cells: ['google_places', 'API oficial; businessStatus, rating y reseñas', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['overture', 'Parquet local; POI y estado', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['phone', 'Llamada manual o proveedor (Vapi, Retell, Dograh)', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['registry', 'Registro oficial por API/convenio', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['news', 'RSS, GNews o NewsAPI', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['yelp', 'API de Yelp', 'Pendiente'],
					status: 'todo'
				},
				{
					cells: ['tripadvisor', 'Terra / Discover', 'Pendiente'],
					status: 'todo'
				}
			]
		},
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
			'Los pesos quality_risk, quality_delta, impact y complaints se leen de la config y entran en la fórmula, pero hoy los dos primeros valen 0 y los dos últimos ni siquiera se suman: la prioridad real se apoya en cierre + antigüedad.'
	},
	{
		id: 5,
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
		id: 6,
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
			rows: [
				{ cells: ['GET /api/report', 'Informe completo', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/report/export', 'El mismo JSON como descarga', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/summary', 'Conteos por tier y estado', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/queue', 'Cola filtrable', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/establishments', 'Lista de establecimientos', 'Implementado'], status: 'done' },
				{ cells: ['POST /api/establishments/{id}/refresh', 'Re-escaneo en segundo plano', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/scan/status', 'Estado del scanner', 'Implementado'], status: 'done' },
				{ cells: ['GET /api/pipeline', 'Las fases del pipeline', 'Implementado'], status: 'done' },
				{ cells: ['GET /metrics', 'Métricas Prometheus', 'Implementado'], status: 'done' },
				{ cells: ['/api/agent/*', 'Info, catálogo y chat SSE', 'Implementado si enabled'], status: 'done' },
				{ cells: ['Autenticación', 'Control de acceso a la API/UI', 'Pendiente'], status: 'todo' }
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
