import Link from 'next/link';
import {
  Activity,
  ArrowRight,
  Boxes,
  Braces,
  Cable,
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
import { FlightRecorder } from '@/components/FlightRecorder';
import { SiteFooter } from '@/components/SiteFooter';
import { SiteHeader } from '@/components/SiteHeader';

const dataPlate = [
  ['Type', 'Self-hosted blackbox monitor'],
  ['Protocols', '18 monitor types'],
  ['Transport', 'NATS JetStream'],
  ['State', 'PostgreSQL'],
  ['Deploy', 'Docker Compose · Helm'],
  ['License', 'AGPL-3.0 open source'],
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
      { name: 'Prometheus query', icon: Activity },
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

export default function HomePage() {
  return (
    <>
      <SiteHeader />
      <main className="marketing-main">
        <section className="hero">
          <div className="section-shell hero__grid">
            <div className="hero__content">
              <p className="hero__eyebrow">Self-hosted / Kubernetes-first / AGPL-3.0</p>
              <h1>
                Monitoring without blind spots<span className="hero__dot">.</span>
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
            </div>
            <dl className="hero__plate" aria-label="Platform summary">
              {dataPlate.map(([term, detail]) => (
                <div key={term}>
                  <dt>{term}</dt>
                  <dd>{detail}</dd>
                </div>
              ))}
            </dl>
          </div>
          <FlightRecorder />
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
                    <div
                      className={`location-node${
                        status === 'Degraded' ? ' location-node--warn' : ''
                      }`}
                      key={place}
                    >
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
                  <div className="alert-row alert-row--warn">
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
                  {[
                    ['Edge', 'Impacted', 'warn'],
                    ['API', 'Impacted', 'warn'],
                    ['Auth', 'Healthy', 'up'],
                    ['DB', 'Root cause', 'down'],
                  ].map(([node, status, tone]) => (
                    <div
                      className={`location-node${
                        tone === 'up' ? '' : ` location-node--${tone}`
                      }`}
                      key={node}
                    >
                      <span>
                        <strong>●</strong>
                        {node}
                        <small>{status}</small>
                      </span>
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
              <span className="section-kicker">18 monitor types</span>
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
