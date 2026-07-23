import Link from 'next/link';
import {
  Activity,
  ArrowRight,
  Boxes,
  Braces,
  Cable,
  Check,
  CircleDot,
  CloudCog,
  Database,
  FileHeart,
  GitBranch,
  Globe2,
  KeyRound,
  Leaf,
  LockKeyhole,
  MapPin,
  MessageSquare,
  MonitorSmartphone,
  Network,
  Radar,
  Radio,
  Search,
  Send,
  Server,
  ShieldCheck,
  Siren,
  Sparkles,
  TerminalSquare,
  Webhook,
  Workflow,
  Zap,
} from 'lucide-react';
import { SiteFooter } from '@/components/SiteFooter';
import { SiteHeader } from '@/components/SiteHeader';

const consoleMonitors = [
  { name: 'Checkout API', type: 'HTTP', latency: '142 ms', status: 'Operational' },
  { name: 'Production Postgres', type: 'PostgreSQL', latency: '18 ms', status: 'Operational' },
  {
    name: 'Login browser flow',
    type: 'Synthetic Browser',
    latency: '1.8 s',
    status: 'Degraded',
  },
  { name: 'EU edge gateway', type: 'Ping', latency: '31 ms', status: 'Operational' },
];

const monitorGroups = [
  {
    label: 'Web & API',
    icon: Globe2,
    monitors: [
      { name: 'HTTP', icon: Globe2 },
      { name: 'WebSocket', icon: Cable },
      { name: 'Synthetic API', icon: Braces },
      { name: 'Synthetic Browser', icon: MonitorSmartphone },
    ],
  },
  {
    label: 'Network',
    icon: Network,
    monitors: [
      { name: 'Ping', icon: Radio },
      { name: 'DNS', icon: Search },
      { name: 'gRPC', icon: Network },
      { name: 'TCP', icon: CircleDot },
      { name: 'SIP', icon: Send },
    ],
  },
  {
    label: 'Databases & brokers',
    icon: Database,
    monitors: [
      { name: 'PostgreSQL', icon: Database },
      { name: 'MySQL', icon: Database },
      { name: 'Redis', icon: Zap },
      { name: 'MongoDB', icon: Leaf },
      { name: 'RabbitMQ', icon: MessageSquare },
    ],
  },
  {
    label: 'Infrastructure & organization',
    icon: Boxes,
    monitors: [
      { name: 'Host agent', icon: Server },
      { name: 'Push heartbeat', icon: Webhook },
      { name: 'Group roll-up', icon: Workflow },
    ],
  },
];

const workflow = [
  {
    title: 'Schedule',
    text: 'The scheduler selects due monitors and publishes location-aware check jobs through NATS JetStream.',
  },
  {
    title: 'Execute',
    text: 'Horizontally scalable workers run protocol, database, browser, and private-network checks, while agents and push endpoints report passive results.',
  },
  {
    title: 'Evaluate',
    text: 'Persisted results drive monitor state, quorum, alert lifecycle, dependency context, and incidents.',
  },
  {
    title: 'Communicate',
    text: 'Route notifications, publish incident updates, and refresh public status pages live.',
  },
];

function ProductConsole() {
  return (
    <div className="hero-console-wrap" aria-label="Illustration of the Probara operations dashboard">
      <div className="hero-console">
        <div className="console-topbar">
          <div className="console-dots" aria-hidden="true">
            <span />
            <span />
            <span />
          </div>
          <span className="console-title mono">Operations overview · production</span>
          <span className="console-live">Live</span>
        </div>
        <div className="console-body">
          <div className="console-main">
            <div className="console-metrics">
              <div className="console-metric">
                <span className="console-metric__label">Fleet uptime</span>
                <div className="console-metric__value">
                  <strong>99.96%</strong>
                  <small>30d</small>
                </div>
              </div>
              <div className="console-metric">
                <span className="console-metric__label">Median latency</span>
                <div className="console-metric__value">
                  <strong>128 ms</strong>
                  <small>−11%</small>
                </div>
              </div>
              <div className="console-metric">
                <span className="console-metric__label">Checks</span>
                <div className="console-metric__value">
                  <strong>48</strong>
                  <small>4 locations</small>
                </div>
              </div>
            </div>
            <div className="console-chart" aria-label="Uptime and latency trend illustration">
              <svg viewBox="0 0 700 120" preserveAspectRatio="none" role="img">
                <defs>
                  <linearGradient id="chartFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#76f0bd" stopOpacity="0.22" />
                    <stop offset="100%" stopColor="#76f0bd" stopOpacity="0" />
                  </linearGradient>
                </defs>
                <path
                  d="M0,75 C50,69 72,56 116,61 C160,66 176,47 222,50 C265,53 294,35 338,42 C390,50 405,30 455,36 C510,44 540,20 584,28 C625,35 664,19 700,21 L700,120 L0,120 Z"
                  fill="url(#chartFill)"
                />
                <path
                  d="M0,75 C50,69 72,56 116,61 C160,66 176,47 222,50 C265,53 294,35 338,42 C390,50 405,30 455,36 C510,44 540,20 584,28 C625,35 664,19 700,21"
                  fill="none"
                  stroke="#76f0bd"
                  strokeWidth="2"
                  vectorEffect="non-scaling-stroke"
                />
                <path
                  d="M0,91 C45,84 82,97 125,85 C174,72 206,88 252,78 C300,67 342,82 390,69 C440,56 475,72 526,62 C585,51 630,62 700,49"
                  fill="none"
                  stroke="#5ad9f8"
                  strokeDasharray="5 5"
                  strokeOpacity="0.65"
                  strokeWidth="1.4"
                  vectorEffect="non-scaling-stroke"
                />
              </svg>
            </div>
            <div className="console-monitor-list">
              {consoleMonitors.map((monitor) => (
                <div className="console-monitor" key={monitor.name}>
                  <span className="console-monitor__name">
                    <span className="console-monitor__icon">
                      <Activity size={12} />
                    </span>
                    {monitor.name}
                  </span>
                  <span className="console-monitor__type">{monitor.type}</span>
                  <span className="console-monitor__latency">{monitor.latency}</span>
                  <span
                    className={`console-status ${
                      monitor.status === 'Degraded' ? 'console-status--warn' : ''
                    }`}
                  >
                    {monitor.status}
                  </span>
                </div>
              ))}
            </div>
          </div>
          <aside className="console-side">
            <h3>Needs attention</h3>
            <div className="console-incident">
              <span className="console-incident__tag">
                <Siren size={11} />
                Investigating
              </span>
              <h4>Elevated login latency in EU</h4>
              <p>2 of 4 locations affected. Dependency context points to the identity service.</p>
            </div>
            <div className="console-timeline">
              <div>
                <strong>Incident created</strong>
                <span>Alert lifecycle · 2m ago</span>
              </div>
              <div>
                <strong>Status page updated</strong>
                <span>Public publication · 1m ago</span>
              </div>
              <div>
                <strong>AI analysis ready</strong>
                <span>3 likely causes · just now</span>
              </div>
            </div>
          </aside>
        </div>
      </div>
    </div>
  );
}

export default function HomePage() {
  return (
    <>
      <SiteHeader />
      <main className="marketing-main">
        <section className="hero">
          <div className="section-shell">
            <div className="hero__content">
              <span className="hero__eyebrow">
                <span className="hero__eyebrow-dot" />
                Self-hosted · Kubernetes-first · GPL-3.0
              </span>
              <h1>
                Monitoring without <span>blind spots.</span>
              </h1>
              <p className="hero__lead">
                Watch endpoints, protocols, databases, hosts, and scripted browser
                journeys from public or private networks—then carry confirmed failures
                through configurable alerts, incidents, and status-page updates.
              </p>
              <div className="hero__actions">
                <Link className="button button--primary" href="/docs/getting-started/">
                  Deploy Probara
                  <ArrowRight size={17} />
                </Link>
                <Link className="button button--secondary" href="/docs/">
                  Explore the docs
                </Link>
              </div>
              <div className="hero__note">
                <span>
                  <Check size={12} /> Docker Compose
                </span>
                <span>
                  <Check size={12} /> Helm chart
                </span>
                <span>
                  <Check size={12} /> External PostgreSQL &amp; NATS
                </span>
              </div>
            </div>
            <ProductConsole />
          </div>
        </section>

        <section className="signal-strip" aria-label="Platform highlights">
          <div className="signal-strip__inner">
            <div className="signal-stat">
              <strong>17</strong>
              <span>monitor types across web, network, data, hosts, and journeys</span>
            </div>
            <div className="signal-stat">
              <strong>Quorum</strong>
              <span>location-aware state that distinguishes down from degraded</span>
            </div>
            <div className="signal-stat">
              <strong>Live</strong>
              <span>status-page updates over NATS and server-sent events</span>
            </div>
            <div className="signal-stat">
              <strong>Own it</strong>
              <span>your infrastructure, telemetry, credentials, and retention policy</span>
            </div>
          </div>
        </section>

        <section className="feature-section" id="platform">
          <div className="section-shell">
            <div className="feature-intro">
              <div>
                <span className="section-kicker">One operating picture</span>
                <h2 className="section-heading">
                  From first signal to <em>final update.</em>
                </h2>
              </div>
              <p className="section-copy">
                Probara connects the parts that usually live in separate tools: check
                execution, distributed location health, alert routing, incident response,
                dependency context, and public communication.
              </p>
            </div>

            <div className="feature-grid">
              <article className="feature-card feature-card--wide">
                <span className="feature-card__icon">
                  <MapPin size={19} />
                </span>
                <h3>See the same service from every network that matters.</h3>
                <p>
                  Deploy NATS-only workers in private locations, assign monitors, set a
                  failure quorum, and use the inter-location mesh to expose broken paths
                  between sites.
                </p>
                <div className="feature-card__visual location-map" aria-hidden="true">
                  {[
                    ['Paris', 'Healthy'],
                    ['Virginia', 'Healthy'],
                    ['Singapore', 'Degraded'],
                    ['Private DC', 'Healthy'],
                  ].map(([place, status]) => (
                    <div className="location-node" key={place}>
                      <span>
                        <strong>●</strong>
                        {place}
                        <small>{status}</small>
                      </span>
                    </div>
                  ))}
                </div>
              </article>

              <article className="feature-card feature-card--narrow">
                <span className="feature-card__icon">
                  <Siren size={19} />
                </span>
                <h3>Alert on state, not noise.</h3>
                <p>
                  Tune consecutive-failure and latency-anomaly sensitivity, route per
                  monitor or by workspace defaults, and mute named maintenance or quick
                  snoozes. Deliver through email, Slack, Discord, Teams, or signed HTTPS
                  webhooks—with delays, reminders, and optional incidents.
                </p>
                <div className="feature-card__visual alert-stack" aria-hidden="true">
                  <div className="alert-row">
                    <span>
                      <i /> Checkout API down
                    </span>
                    <small>Slack · Discord</small>
                  </div>
                  <div className="alert-row">
                    <span>
                      <i /> Identity latency anomaly
                    </span>
                    <small>Webhook</small>
                  </div>
                  <div className="alert-row">
                    <span>
                      <i /> Private DC mesh edge
                    </span>
                    <small>Teams</small>
                  </div>
                </div>
              </article>

              <article className="feature-card feature-card--third">
                <span className="feature-card__icon">
                  <FileHeart size={19} />
                </span>
                <h3>Status pages that stay in the loop.</h3>
                <p>
                  Group services into sections, publish incident updates, theme the
                  built-in page, or own the entire Go template with draft preview,
                  versioned publish and revert, and a reusable template library.
                </p>
                <div className="feature-card__visual status-mini" aria-hidden="true">
                  <div className="status-mini__head">
                    Acme service status
                    <span className="status-mini__badge">All systems operational</span>
                  </div>
                  <div className="status-mini__services">
                    {['API', 'Dashboard', 'Worker fleet'].map((service) => (
                      <div key={service}>
                        <span>{service}</span>
                        <span className="status-mini__bars">
                          {Array.from({ length: 18 }).map((_, index) => (
                            <i key={index} />
                          ))}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              </article>

              <article className="feature-card feature-card--third">
                <span className="feature-card__icon">
                  <GitBranch size={19} />
                </span>
                <h3>Context for the failure, not just the symptom.</h3>
                <p>
                  Model service dependencies, annotate downstream alerts, explore
                  co-firing signals, and ask the configured LLM for incident root causes.
                </p>
                <div className="feature-card__visual location-map" aria-hidden="true">
                  {['Edge', 'API', 'Auth', 'DB'].map((node) => (
                    <div className="location-node" key={node}>
                      <strong>●</strong>
                      {node}
                    </div>
                  ))}
                </div>
              </article>

              <article className="feature-card feature-card--third">
                <span className="feature-card__icon">
                  <ShieldCheck size={19} />
                </span>
                <h3>Built for shared operations.</h3>
                <p>
                  Separate tenants, control writes with roles and scoped API keys, connect
                  OIDC, retain an audit trail, and configure encryption for stored monitor
                  and channel secrets.
                </p>
                <div className="feature-card__visual rbac-grid" aria-hidden="true">
                  <div className="rbac-cell">
                    <span>
                      <strong>Admin</strong>
                      Configure
                    </span>
                  </div>
                  <div className="rbac-cell">
                    <span>
                      <strong>Editor</strong>
                      Configure &amp; respond
                    </span>
                  </div>
                  <div className="rbac-cell">
                    <span>
                      <strong>Viewer</strong>
                      Observe
                    </span>
                  </div>
                </div>
              </article>
            </div>
          </div>
        </section>

        <section className="monitor-section" id="monitoring">
          <div className="section-shell monitor-layout">
            <div className="monitor-layout__sticky">
              <span className="section-kicker">17 monitor types</span>
              <h2 className="section-heading">
                Check the protocol, <em>not a proxy for it.</em>
              </h2>
              <p className="section-copy">
                Verify availability at the layer users and systems actually consume—from
                HTTP body, header, JSON, latency, and TLS-expiry assertions to extracted
                multi-step API variables, scripted browser journeys, database
                authentication, AMQP, ICMP, DNS records, host telemetry, and dead-man
                heartbeats.
              </p>
              <div style={{ marginTop: '1.6rem' }}>
                <Link className="button button--ghost" href="/docs/monitors/">
                  Read the monitor reference
                  <ArrowRight size={15} />
                </Link>
              </div>
            </div>
            <div className="monitor-groups">
              {monitorGroups.map((group) => {
                const GroupIcon = group.icon;
                return (
                  <article className="monitor-group" key={group.label}>
                    <h3>
                      <GroupIcon size={14} />
                      {group.label}
                    </h3>
                    <div className="monitor-pills">
                      {group.monitors.map((monitor) => {
                        const Icon = monitor.icon;
                        return (
                          <span className="monitor-pill" key={monitor.name}>
                            <Icon size={14} />
                            {monitor.name}
                          </span>
                        );
                      })}
                    </div>
                  </article>
                );
              })}
            </div>
          </div>
        </section>

        <section className="architecture-section">
          <div className="section-shell">
            <span className="section-kicker">System design</span>
            <h2 className="section-heading">
              Scale the work, <em>keep one source of truth.</em>
            </h2>
            <p className="section-copy">
              Stateless Go services divide responsibilities cleanly. PostgreSQL holds the
              application model; NATS JetStream carries durable work and events; workers
              grow independently with check volume.
            </p>

            <div className="architecture-diagram">
              <div className="architecture-column">
                <p className="architecture-column__label">Inputs</p>
                <div className="architecture-node">
                  <h3>
                    <Globe2 size={15} /> Web UI &amp; API clients
                  </h3>
                  <p>CRUD, run-now, imports, configuration, and response workflows.</p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <Server size={15} /> Host agents &amp; pushes
                  </h3>
                  <p>Machine telemetry and externally initiated heartbeat results.</p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <MapPin size={15} /> Private workers
                  </h3>
                  <p>Location-scoped execution with NATS-only result transport.</p>
                </div>
              </div>

              <div className="architecture-core">
                <div className="architecture-core__title">
                  <strong>Probara control plane</strong>
                  <span>Horizontally scalable</span>
                </div>
                <div className="architecture-node">
                  <h3>
                    <CloudCog size={15} /> API · Scheduler · Workers
                  </h3>
                  <p>Create state, schedule due checks, execute work, and persist results.</p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <Database size={15} /> PostgreSQL + NATS
                  </h3>
                  <p>
                    Durable application state, JetStream queues for check jobs and alerts,
                    and core NATS for live updates.
                  </p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <Radar size={15} /> Alerter · Status page
                  </h3>
                  <p>State-driven response and a separately served public experience.</p>
                </div>
              </div>

              <div className="architecture-column">
                <p className="architecture-column__label">Outcomes</p>
                <div className="architecture-node">
                  <h3>
                    <Activity size={15} /> Dashboard &amp; analytics
                  </h3>
                  <p>Live state, trends, grouped services, history, and detailed results.</p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <Siren size={15} /> Alerts &amp; incidents
                  </h3>
                  <p>Deduplicated lifecycle, routing, timelines, and analysis.</p>
                </div>
                <div className="architecture-node">
                  <h3>
                    <FileHeart size={15} /> Public status
                  </h3>
                  <p>Cached pages, incidents, maintenance, and live SSE refresh.</p>
                </div>
              </div>
            </div>

            <div className="workflow">
              {workflow.map((step, index) => (
                <article className="workflow-step" key={step.title}>
                  <span className="workflow-step__number">0{index + 1}</span>
                  <h3>{step.title}</h3>
                  <p>{step.text}</p>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section className="deployment-section" id="deployment">
          <div className="section-shell deployment-layout">
            <div>
              <span className="section-kicker">Deployment</span>
              <h2 className="section-heading">
                Your stack. <em>Your boundaries.</em>
              </h2>
              <p className="section-copy">
                Start the complete development stack with one command, run application
                services locally against containerized infrastructure, or install the
                included Helm chart in Kubernetes.
              </p>
              <div className="deployment-points">
                <div className="deployment-point">
                  <LockKeyhole size={16} />
                  Host application data and stored credentials inside infrastructure you
                  control.
                </div>
                <div className="deployment-point">
                  <CloudCog size={16} />
                  Bring external PostgreSQL and NATS or deploy the bundled dependencies.
                </div>
                <div className="deployment-point">
                  <Sparkles size={16} />
                  Scale worker replicas separately and enable the Helm worker HPA.
                </div>
                <div className="deployment-point">
                  <KeyRound size={16} />
                  Add OIDC SSO, scoped API keys, secret encryption, and private locations.
                </div>
              </div>
            </div>
            <div className="terminal">
              <div className="terminal__head">
                <span className="mono">probara / quick start</span>
                <TerminalSquare size={14} />
              </div>
              <pre>
                <span className="comment"># Clone and prepare configuration</span>
                {'\n'}
                <span className="command">git clone</span>{' '}
                https://github.com/yassinebenameur/probara.git{'\n'}
                <span className="command">cd</span> probara{'\n'}
                <span className="command">cp</span> .env.example .env{'\n\n'}
                <span className="comment">
                  # Set ADMIN_JWT_SECRET (32+ random characters) and PUBLIC_BASE_URL in
                  .env
                </span>
                {'\n\n'}
                <span className="comment"># Local Go services + Docker infrastructure</span>
                {'\n'}
                <span className="command">make</span>{' '}
                <span className="value">start-all-local</span>
                {'\n\n'}
                <span className="comment"># Or Docker-backed application services</span>
                {'\n'}
                <span className="command">make</span>{' '}
                <span className="value">start-all</span>
                {'\n\n'}
                <span className="comment"># Verify the main Go module</span>
                {'\n'}
                <span className="command">make</span> test{'\n'}
                <span className="command">make</span> lint
              </pre>
            </div>
          </div>
        </section>

        <section className="cta-section">
          <div className="section-shell">
            <div className="cta-panel">
              <h2>Own the signal. Shorten the incident.</h2>
              <p>
                Deploy the complete stack, connect the systems that matter, and keep the
                operational picture in your hands.
              </p>
              <div className="cta-panel__actions">
                <Link className="button button--primary" href="/docs/getting-started/">
                  Start deploying
                  <ArrowRight size={17} />
                </Link>
                <a
                  className="button button--secondary"
                  href="https://github.com/yassinebenameur/probara"
                  target="_blank"
                  rel="noreferrer"
                >
                  View on GitHub
                </a>
              </div>
            </div>
          </div>
        </section>
      </main>
      <SiteFooter />
    </>
  );
}
