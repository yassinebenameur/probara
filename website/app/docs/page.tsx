import type { Metadata } from 'next';
import Link from 'next/link';
import {
  ArrowRight,
  BellRing,
  BookOpen,
  Boxes,
  Code2,
  KeyRound,
  MapPin,
  Rocket,
  Settings2,
  ShieldCheck,
  Stethoscope,
} from 'lucide-react';
import { DOC_NAVIGATION } from '@/lib/docs/navigation';
import { CodeBlock } from '@/components/docs/CodeBlock';

export const metadata: Metadata = {
  title: 'Documentation',
  description:
    'Complete Probara documentation for monitoring, alerting, incidents, status pages, APIs, configuration, deployment, operations, and security.',
};

const paths = [
  {
    title: 'Deploy the platform',
    text: 'Choose the local-process, Docker Compose, or Helm path and understand required secrets.',
    href: '/docs/getting-started/',
    icon: Rocket,
  },
  {
    title: 'Build your monitor fleet',
    text: 'Configure all 18 monitor types, locations, quorum, dependencies, agents, and pushes.',
    href: '/docs/monitors/',
    icon: Stethoscope,
  },
  {
    title: 'Design incident response',
    text: 'Set sensitivity, route notifications, suppress maintenance, and publish incidents.',
    href: '/docs/alerting/',
    icon: BellRing,
  },
  {
    title: 'Harden production',
    text: 'Review network destination policy, secret encryption, SSO, NATS, and tenant isolation.',
    href: '/docs/security/',
    icon: ShieldCheck,
  },
];

export default function DocsOverviewPage() {
  return (
    <div className="docs-overview">
      <div className="docs-breadcrumb">
        <span>Docs</span>
        <span>/</span>
        <span>Overview</span>
      </div>
      <header className="docs-overview__hero">
        <span className="docs-eyebrow">Probara documentation</span>
        <h1>Operate the entire monitoring lifecycle.</h1>
        <p>
          Learn the product from architecture through production operations. This
          reference follows the current development state and documents implemented
          behavior, configuration, and boundaries.
        </p>
      </header>

      <section className="docs-quickstart">
        <div>
          <span className="docs-section-label">Fast path</span>
          <h2>Run the local stack.</h2>
          <p>
            The local-process workflow starts PostgreSQL and NATS in Docker, then runs
            migrations, Go services, downloadable agent builds, and the UI.
          </p>
          <Link href="/docs/getting-started/">
            Follow the complete quick start
            <ArrowRight size={14} />
          </Link>
        </div>
        <CodeBlock
          language="shell"
          title="Terminal"
          code={`git clone https://github.com/yassinebenameur/probara.git
cd probara
cp .env.example .env
# Set ADMIN_JWT_SECRET (32+ chars) and PUBLIC_BASE_URL in .env
make start-all-local`}
        />
      </section>

      <section className="docs-overview__section">
        <span className="docs-section-label">Choose a path</span>
        <h2>What are you here to do?</h2>
        <div className="docs-path-grid">
          {paths.map((path) => {
            const Icon = path.icon;
            return (
              <Link href={path.href} key={path.title}>
                <span className="docs-path-grid__icon">
                  <Icon size={18} />
                </span>
                <h3>{path.title}</h3>
                <p>{path.text}</p>
                <ArrowRight className="docs-path-grid__arrow" size={15} />
              </Link>
            );
          })}
        </div>
      </section>

      <section className="docs-overview__section">
        <span className="docs-section-label">Full reference</span>
        <h2>Browse all documentation.</h2>
        <div className="docs-index-grid">
          {DOC_NAVIGATION.map((group, groupIndex) => {
            const icons = [BookOpen, Boxes, Settings2];
            const Icon = icons[groupIndex] || Code2;
            return (
              <div className="docs-index-group" key={group.title}>
                <h3>
                  <Icon size={16} />
                  {group.title}
                </h3>
                {group.items.map((item) => (
                  <Link href={`/docs/${item.slug}/`} key={item.slug}>
                    <span>
                      <strong>{item.title}</strong>
                      <small>{item.description}</small>
                    </span>
                    <ArrowRight size={13} />
                  </Link>
                ))}
              </div>
            );
          })}
        </div>
      </section>

      <section className="docs-reference-strip">
        <div>
          <MapPin size={18} />
          <span>
            <strong>Private locations</strong>
            NATS-only workers, result ingest, quorum, and mesh
          </span>
        </div>
        <div>
          <KeyRound size={18} />
          <span>
            <strong>Every variable</strong>
            Service, default, accepted values, and operational effect
          </span>
        </div>
        <div>
          <Code2 size={18} />
          <span>
            <strong>API conventions</strong>
            Authentication, tenancy, pagination, and endpoint families
          </span>
        </div>
      </section>
    </div>
  );
}
