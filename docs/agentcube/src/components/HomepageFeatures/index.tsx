import type { ReactNode } from "react";
import clsx from "clsx";
import Heading from "@theme/Heading";
import styles from "./styles.module.css";

type FeatureItem = {
  title: string;
  icon: ReactNode;
  description: ReactNode;
};

const LowLatencyIcon = () => (
  <svg
    viewBox="0 0 24 24"
    className={styles.featureSvg}
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M12 2a10 10 0 0 1 10 10" strokeDasharray="2 2" />
    <path d="M22 12a10 10 0 0 1-18 6" />
    <path d="M12 2a10 10 0 0 0-8 4" />
    <circle cx="12" cy="12" r="6" />
    <polyline points="12 9 12 12 14.5 13.5" />
    <line x1="2" y1="8" x2="6" y2="8" />
    <line x1="1" y1="12" x2="4" y2="12" />
    <line x1="3" y1="16" x2="7" y2="16" />
  </svg>
);

const StatefulLifecycleIcon = () => (
  <svg
    viewBox="0 0 24 24"
    className={styles.featureSvg}
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <polygon points="12 2 22 7 12 12 2 7" />
    <polygon points="2 7 12 12 12 22 2 17" />
    <polygon points="12 12 22 7 22 17 12 22" />
    <line x1="12" y1="12" x2="12" y2="22" />
    <line x1="12" y1="7" x2="2" y2="12" />
    <line x1="12" y1="7" x2="22" y2="12" />
    <path d="M4 11.5 L12 15.5 M12 15.5 L20 11.5" />
    <path d="M6 16.5 L12 19.5 M12 19.5 L18 16.5" />
  </svg>
);

const ResourceUtilizationIcon = () => (
  <svg
    viewBox="0 0 24 24"
    className={styles.featureSvg}
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <circle cx="12" cy="12" r="3" fill="currentColor" fillOpacity="0.1" />
    <circle cx="12" cy="4" r="2" />
    <circle cx="4" cy="18" r="2" />
    <circle cx="20" cy="18" r="2" />
    <line x1="12" y1="6" x2="12" y2="9" />
    <line x1="5.5" y1="16.5" x2="10" y2="13.5" />
    <line x1="18.5" y1="16.5" x2="14" y2="13.5" />
    <path d="M12 2 L22 17 L2 17 Z" strokeDasharray="2 2" />
    <line x1="8" y1="21" x2="16" y2="21" strokeWidth="2" />
    <line
      x1="10"
      y1="21"
      x2="14"
      y2="21"
      stroke="var(--brand-red)"
      strokeWidth="2"
    />
  </svg>
);

const FeatureList: FeatureItem[] = [
  {
    title: "Low-Latency Agent Scheduling",
    icon: <LowLatencyIcon />,
    description: (
      <>
        Fast startup and resume for interactive AI agents. Optimized scheduling
        paths enable sub-second agent startup and rapid resume from idle states.
      </>
    ),
  },
  {
    title: "Stateful Agent Lifecycle",
    icon: <StatefulLifecycleIcon />,
    description: (
      <>
        Built-in state preservation and sleep/resume semantics. Agents retain
        context across long-running sessions while idle resources are safely
        released.
      </>
    ),
  },
  {
    title: "Efficient Resource Utilization",
    icon: <ResourceUtilizationIcon />,
    description: (
      <>
        High-density placement with performance isolation. Advanced bin-packing
        maximizes cluster utilization under strict resource and isolation
        guarantees.
      </>
    ),
  },
];

function Feature({ title, icon, description }: FeatureItem) {
  return (
    <div className={clsx("col col--4")}>
      <div className={styles.featureCard}>
        <div className={styles.featureIconContainer}>{icon}</div>
        <div className={styles.featureContent}>
          <Heading as="h3" className={styles.featureCardTitle}>
            {title}
          </Heading>
          <p className={styles.featureCardDescription}>{description}</p>
        </div>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
