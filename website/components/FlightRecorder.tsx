const CHANNELS = [
  {
    id: 'CH 01',
    name: 'checkout-api',
    type: 'HTTP',
    value: '142 ms',
    tone: 'incident' as const,
    path: 'M0,38 L28,36 L52,39 L80,35 L104,37 L132,33 L158,36 L186,34 L214,37 L242,33 L268,35 L296,31 L322,34 L350,32 L378,35 L404,31 L432,33 L460,30 L488,33 L514,29 L542,32 L570,28 L598,24 L612,14 L620,6 L700,6 L710,20 L724,30 L752,28 L780,31 L806,28 L834,30 L862,27 L890,29 L916,26 L944,28 L972,25 L1000,27',
    segments: [
      { from: 0, to: 59.8, tone: 'up' },
      { from: 59.8, to: 71, tone: 'down' },
      { from: 71, to: 100, tone: 'up' },
    ],
  },
  {
    id: 'CH 02',
    name: 'prod-postgres',
    type: 'PostgreSQL',
    value: '18 ms',
    tone: 'steady' as const,
    path: 'M0,42 L36,41 L70,43 L108,41 L142,42 L178,40 L214,42 L250,41 L286,43 L322,41 L358,42 L394,40 L430,41 L466,42 L502,40 L538,41 L574,42 L610,44 L646,42 L682,41 L718,42 L754,40 L790,41 L826,42 L862,40 L898,41 L934,42 L970,41 L1000,42',
    segments: [{ from: 0, to: 100, tone: 'up' }],
  },
  {
    id: 'CH 03',
    name: 'login-journey',
    type: 'Browser',
    value: '1.8 s',
    tone: 'degraded' as const,
    path: 'M0,40 L34,38 L68,41 L102,37 L136,39 L170,36 L204,38 L238,35 L272,37 L306,34 L340,36 L374,33 L408,35 L442,31 L476,33 L510,30 L544,31 L578,27 L612,29 L646,24 L680,26 L714,21 L748,23 L782,18 L816,20 L850,16 L884,18 L918,14 L952,16 L1000,12',
    segments: [
      { from: 0, to: 68, tone: 'up' },
      { from: 68, to: 100, tone: 'warn' },
    ],
  },
  {
    id: 'CH 04',
    name: 'eu-edge',
    type: 'Ping',
    value: '31 ms',
    tone: 'steady' as const,
    path: 'M0,39 L40,41 L78,38 L116,40 L154,37 L192,40 L230,38 L268,41 L306,38 L344,40 L382,37 L420,39 L458,41 L496,38 L534,40 L572,37 L610,40 L648,38 L686,41 L724,38 L762,40 L800,37 L838,39 L876,41 L914,38 L952,40 L1000,38',
    segments: [{ from: 0, to: 100, tone: 'up' }],
  },
];

const TIME_MARKS = ['14:00', '14:10', '14:20', '14:30', '14:40', 'NOW'];

export function FlightRecorder() {
  return (
    <div
      className="recorder"
      role="img"
      aria-label="Illustration of a multi-channel monitoring trace: four monitors recorded over time, with an outage on the checkout API carried through alert, incident, and status-page update"
    >
      <div className="recorder__inner">
        <div className="recorder__head">
          <span className="recorder__title">Continuous record · 4 monitors · 4 locations</span>
          <span className="recorder__live">
            <i />
            Recording
          </span>
        </div>

        <div className="recorder__annotation-track">
          <div className="recorder__annotation" style={{ left: '59.8%' }}>
            <span className="recorder__annotation-flag recorder__annotation-flag--full">
              14:32 · quorum 2/4 → alert → incident #482 → status page
            </span>
            <span className="recorder__annotation-flag recorder__annotation-flag--short">
              14:32 · incident #482
            </span>
          </div>
        </div>

        {CHANNELS.map((channel, index) => (
          <div className={`recorder-channel recorder-channel--${channel.tone}`} key={channel.id}>
            <div className="recorder-channel__rail">
              <span className="recorder-channel__id">{channel.id}</span>
              <span className="recorder-channel__name">{channel.name}</span>
              <span className="recorder-channel__meta">
                {channel.type} · {channel.value}
              </span>
            </div>
            <div className="recorder-channel__trace">
              <svg viewBox="0 0 1000 60" preserveAspectRatio="none" aria-hidden="true">
                <path
                  d={channel.path}
                  pathLength={1}
                  className="recorder-channel__line"
                  style={{ animationDelay: `${140 * index}ms` }}
                  fill="none"
                  vectorEffect="non-scaling-stroke"
                />
              </svg>
              <div className="recorder-channel__ribbon" aria-hidden="true">
                {channel.segments.map((segment) => (
                  <i
                    key={`${channel.id}-${segment.from}`}
                    className={`recorder-segment recorder-segment--${segment.tone}`}
                    style={{
                      left: `${segment.from}%`,
                      width: `${segment.to - segment.from}%`,
                    }}
                  />
                ))}
              </div>
            </div>
          </div>
        ))}

        <div className="recorder__axis" aria-hidden="true">
          <span className="recorder__axis-rail" />
          {TIME_MARKS.map((mark) => (
            <span key={mark} className={mark === 'NOW' ? 'recorder__now' : undefined}>
              {mark}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}
