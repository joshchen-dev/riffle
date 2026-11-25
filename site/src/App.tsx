import type { ReactNode } from 'react';
import {
  ActionIcon,
  Anchor,
  Badge,
  Box,
  Button,
  Card,
  Container,
  Group,
  SimpleGrid,
  Stack,
  Tabs,
  Text,
  ThemeIcon,
  Title,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core';
import {
  IconBolt,
  IconBrandGithub,
  IconChartLine,
  IconCloudUpload,
  IconEyeOff,
  IconFileCode,
  IconFilter,
  IconGitBranch,
  IconMoon,
  IconServer,
  IconShieldLock,
  IconSun,
  IconTerminal2,
  IconWebhook,
} from '@tabler/icons-react';

const REPO = 'https://github.com/joshchen-dev/riffle';
const blob = (path: string) => `${REPO}/blob/main/${path}`;

function Term({ children }: { children: ReactNode }) {
  return <pre className="term">{children}</pre>;
}

function Cmd({ children }: { children: string }) {
  return (
    <>
      <span className="prompt">$ </span>
      {children}
      {'\n'}
    </>
  );
}

function Section({ id, alt, title, lead, children }: {
  id?: string;
  alt?: boolean;
  title: string;
  lead?: string;
  children: ReactNode;
}) {
  return (
    <Box component="section" id={id} py={{ base: 56, md: 88 }} className={alt ? 'section-alt' : undefined}>
      <Container size="lg">
        <Stack gap="xs" mb={40} maw={720}>
          <Title order={2}>{title}</Title>
          {lead && <Text c="dimmed" size="lg">{lead}</Text>}
        </Stack>
        {children}
      </Container>
    </Box>
  );
}

function Header() {
  const { setColorScheme } = useMantineColorScheme();
  const scheme = useComputedColorScheme('light');
  return (
    <Box component="header" className="header">
      <Container size="lg" h={60}>
        <Group h="100%" justify="space-between">
          <Anchor href="#" underline="never" c="inherit">
            <Group gap={8}>
              <img src="/favicon.svg" width={26} height={26} alt="" />
              <Text fw={700} size="lg">Riffle</Text>
            </Group>
          </Anchor>
          <Group gap="lg">
            <Group gap="lg" visibleFrom="sm">
              {[['How it works', '#how'], ['Features', '#features'], ['CLI', '#cli'], ['Quickstart', '#quickstart']].map(([label, href]) => (
                <Anchor key={href} href={href} c="dimmed" size="sm" underline="never">{label}</Anchor>
              ))}
            </Group>
            <ActionIcon
              variant="default"
              size="lg"
              aria-label="Toggle color scheme"
              onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}
            >
              {scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
            </ActionIcon>
            <Button component="a" href={REPO} variant="default" leftSection={<IconBrandGithub size={18} />} visibleFrom="sm">
              GitHub
            </Button>
            <ActionIcon component="a" href={REPO} variant="default" size="lg" aria-label="GitHub" hiddenFrom="sm">
              <IconBrandGithub size={18} />
            </ActionIcon>
          </Group>
        </Group>
      </Container>
    </Box>
  );
}

const ALERT_YAML = `apiVersion: riffle.jhchen.dev/v1alpha1
kind: LogAlert
metadata: {name: api-timeouts, namespace: payments}
spec:
  selector: {matchLabels: {app: api}}
  match: 'timeout'
  where: 'level=error AND latency_ms>500'
  threshold: {count: 20, window: 5m}
  redact: ['card=\\d+']`;

function Hero() {
  return (
    <Container size="lg" py={{ base: 56, md: 96 }}>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing={48} style={{ alignItems: 'center' }}>
        <Stack gap="lg">
          <Group gap="xs">
            <Badge variant="light">Open source · Apache 2.0</Badge>
            <Badge variant="light" color="gray">Kubernetes operator + CLI</Badge>
          </Group>
          <Title className="hero-title">
            Log alerts as{' '}
            <Text span inherit variant="gradient" gradient={{ from: 'cyan.6', to: 'teal.5' }}>
              Kubernetes resources
            </Text>
          </Title>
          <Text size="xl" c="dimmed">
            Declare a <Code>LogAlert</Code>. Riffle matches it against pod logs on every node and, when the
            threshold is crossed, sets <Code>Firing</Code>, records an Event and calls your webhook in about
            half a second.
          </Text>
          <Group>
            <Button component="a" href="#quickstart" size="md">Get started</Button>
            <Button component="a" href={REPO} size="md" variant="default" leftSection={<IconBrandGithub size={18} />}>
              View on GitHub
            </Button>
          </Group>
        </Stack>
        <Term>
          <span className="dim"># api-timeouts.yaml{'\n'}</span>
          {ALERT_YAML}
        </Term>
      </SimpleGrid>
    </Container>
  );
}

function Code({ children }: { children: ReactNode }) {
  return <Text span ff="monospace" fz="0.92em">{children}</Text>;
}

function Latency() {
  const stats = [['502 ms', 'p50'], ['521 ms', 'p90'], ['524 ms', 'max']];
  return (
    <Section alt title="From log line to webhook in half a second" lead="Measured end to end on k3d: a pod writes a line, Riffle's webhook arrives. 20 trials.">
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        {stats.map(([value, label]) => (
          <Card key={label} withBorder padding="xl">
            <Text className="stat">{value}</Text>
            <Text c="dimmed">{label}</Text>
          </Card>
        ))}
      </SimpleGrid>
      <Text size="sm" c="dimmed" mt="md">
        Reproduce it with{' '}
        <Anchor href={blob('hack/bench-latency.sh')}>hack/bench-latency.sh</Anchor> on any cluster running Riffle.
      </Text>
    </Section>
  );
}

function Why() {
  const cards = [
    { title: 'stern, k9s', body: 'Good at tailing logs, but they don’t keep rules.' },
    { title: 'Loki', body: 'Stores and searches logs, which means running a log store.' },
    {
      title: 'Riffle',
      body: 'Rules are namespaced CRDs, reviewed in Git, scoped by RBAC and deployed with Argo CD. Only matches leave the node.',
      highlight: true,
    },
  ];
  return (
    <Section title="Between a tail and a log store" lead="Riffle keeps the rules without keeping the logs.">
      <SimpleGrid cols={{ base: 1, md: 3 }}>
        {cards.map((c) => (
          <Card key={c.title} withBorder padding="xl" style={c.highlight ? { borderColor: 'var(--mantine-color-cyan-6)' } : undefined}>
            <Text fw={700} size="lg" mb="xs">{c.title}</Text>
            <Text c="dimmed">{c.body}</Text>
          </Card>
        ))}
      </SimpleGrid>
    </Section>
  );
}

function HowItWorks() {
  const steps = [
    ['Tail', 'A DaemonSet agent follows /var/log/pods, read-only, for pods a LogAlert selects.'],
    ['Match', 'Each line runs through match and where, then redaction. Matches are counted in 10s buckets.'],
    ['Aggregate', 'Agents push counts to the operator, which sums them across nodes over each rule’s window.'],
    ['Alert', 'Over the threshold: Firing=True, a Warning Event, a webhook. Below it again: resolved.'],
  ];
  return (
    <Section id="how" alt title="How it works" lead="An operator, a node agent and a CLI sharing one filter engine.">
      <Box component="img" src="/architecture.svg" alt="Riffle architecture: Git and kubectl, the API server, the riffle CLI, the operator, a node agent per node reading /var/log/pods, Prometheus, Slack and an S3 archive" className="diagram" />
      <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} mt={40}>
        {steps.map(([title, body], i) => (
          <Stack key={title} gap={6}>
            <Text c="cyan" fw={700} size="sm">{String(i + 1).padStart(2, '0')}</Text>
            <Text fw={700}>{title}</Text>
            <Text c="dimmed" size="sm">{body}</Text>
          </Stack>
        ))}
      </SimpleGrid>
    </Section>
  );
}

function Features() {
  const features = [
    [IconFileCode, 'Rules as CRDs', 'Namespaced LogAlerts with CEL validation and Ready / Firing conditions in kubectl get.'],
    [IconFilter, 'One filter engine', 'RE2 match plus a where language for JSON fields, shared by the agent and the CLI.'],
    [IconEyeOff, 'Redaction everywhere', 'Redact patterns apply to everything Riffle emits: Events, webhooks, the S3 archive and CLI output.'],
    [IconBolt, 'Cluster-wide thresholds', 'Counts from every node are summed over a sliding window from 1m to 1h.'],
    [IconWebhook, 'Webhook alerts', 'Slack-compatible JSON on firing and resolved, with the redacted sample line.'],
    [IconChartLine, 'Prometheus metrics', 'riffle_matches_total per namespace, rule and container, plus lines and bytes read.'],
    [IconCloudUpload, 'S3 archive', 'Redacted matches as gzipped JSON Lines, partitioned for Athena. Bucket and IAM in Terraform.'],
    [IconShieldLock, 'Least privilege', 'Read-only agent; counts authenticated with an audience-bound ServiceAccount token and TokenReview.'],
    [IconGitBranch, 'GitOps-ready', 'One manifest bootstraps Argo CD on k3s; an Ansible playbook sets up RHEL with SELinux enforcing.'],
  ] as const;
  return (
    <Section id="features" title="Features">
      <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }} spacing="xl" verticalSpacing={36}>
        {features.map(([Icon, title, body]) => (
          <Stack key={title} gap="xs">
            <ThemeIcon variant="light" size={40} radius="md">
              <Icon size={22} />
            </ThemeIcon>
            <Text fw={700}>{title}</Text>
            <Text c="dimmed" size="sm">{body}</Text>
          </Stack>
        ))}
      </SimpleGrid>
    </Section>
  );
}

function Cli() {
  return (
    <Section id="cli" alt title="A CLI that speaks the same rules" lead="Tail many pods as one timeline, filter JSON fields, then turn the filter into an alert. Works on any cluster; the operator is optional.">
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="xl">
        <Stack gap="xs">
          <Text fw={600}>Filter JSON logs across pods</Text>
          <Term>
            <Cmd>{`riffle tail -n payments -l app=api --where 'level=error AND latency_ms>500'`}</Cmd>
            {`api-7d9f-x2k4p/api {"level":"error","latency_ms":604,"msg":"upstream timeout"}
api-7d9f-hw6fk/api {"level":"error","latency_ms":715,"msg":"upstream timeout"}`}
          </Term>
        </Stack>
        <Stack gap="xs">
          <Text fw={600}>Tail a rule, redacted</Text>
          <Term>
            <Cmd>riffle tail -n payments --rule api-timeouts</Cmd>
            {`api-7d9f-x2k4p/api {"level":"error","latency_ms":604,"msg":"upstream timeout",[REDACTED]}`}
          </Term>
        </Stack>
        <Stack gap="xs">
          <Text fw={600}>Turn a filter into a rule</Text>
          <Term>
            <Cmd>{`riffle alert new api-timeouts -n payments -l app=api --where 'level=error AND latency_ms>500' --count 20 > api-timeouts.yaml`}</Cmd>
            <Cmd>riffle alerts -A</Cmd>
            {`NAMESPACE   NAME           READY   FIRING   PODS   THRESHOLD   LAST MATCH
payments    api-timeouts   True    True     3      20/5m0s     4s ago`}
          </Term>
        </Stack>
        <Stack gap="xs">
          <Text fw={600}>Edit the filter live</Text>
          <Text c="dimmed" size="sm">
            <Code>riffle tail -i</Code> opens an interactive view: edit <Code>match</Code> and <Code>where</Code> over
            the last 10,000 lines, then <Code>ctrl+s</Code> prints the filter as LogAlert YAML.
          </Text>
        </Stack>
      </SimpleGrid>
    </Section>
  );
}

function Quickstart() {
  return (
    <Section id="quickstart" title="Quickstart">
      <Tabs defaultValue="k3d" variant="pills">
        <Tabs.List mb="md">
          <Tabs.Tab value="k3d" leftSection={<IconServer size={16} />}>Local (k3d)</Tabs.Tab>
          <Tabs.Tab value="gitops" leftSection={<IconGitBranch size={16} />}>GitOps (k3s)</Tabs.Tab>
          <Tabs.Tab value="cli" leftSection={<IconTerminal2 size={16} />}>CLI only</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="k3d">
          <Term>
            <Cmd>git clone https://github.com/joshchen-dev/riffle && cd riffle</Cmd>
            <Cmd>k3d cluster create riffle</Cmd>
            <Cmd>make docker-build IMG=riffle-operator:dev</Cmd>
            <Cmd>k3d image import riffle-operator:dev -c riffle</Cmd>
            <Cmd>make deploy IMG=riffle-operator:dev   # operator + node agent</Cmd>
            <Cmd>kubectl apply -n default -f config/samples/riffle_v1alpha1_logalert.yaml</Cmd>
          </Term>
        </Tabs.Panel>
        <Tabs.Panel value="gitops">
          <Term>
            <span className="dim"># k3s installs Argo CD, which syncs Riffle and rules for the cluster's own components.{'\n'}</span>
            <Cmd>kubectl apply -f https://raw.githubusercontent.com/joshchen-dev/riffle/main/deploy/bootstrap/argocd.yaml</Cmd>
          </Term>
          <Text size="sm" c="dimmed" mt="sm">
            Starting from a bare RHEL 9 host? <Anchor href={blob('ansible/README.md')}>The Ansible playbook</Anchor>{' '}
            installs k3s with SELinux enforcing and does this step for you.
          </Text>
        </Tabs.Panel>
        <Tabs.Panel value="cli">
          <Term>
            <Cmd>go install github.com/joshchen-dev/riffle/cmd/riffle@latest</Cmd>
            <Cmd>riffle tail -n default -l app=api --where 'level=error'</Cmd>
          </Term>
        </Tabs.Panel>
      </Tabs>
    </Section>
  );
}

function OpenSource() {
  const stack = ['Go', 'controller-runtime', 'CRDs + CEL', 'envtest', 'Prometheus', 'bubbletea', 'Terraform', 'AWS S3', 'Argo CD', 'Ansible', 'GitHub Actions'];
  return (
    <Section alt title="Open source" lead="Apache 2.0. No sign-up, no hosted tier: it runs in your cluster.">
      <Group gap="xs" mb="xl">
        {stack.map((s) => <Badge key={s} variant="outline" color="gray" size="lg" tt="none">{s}</Badge>)}
      </Group>
      <Group>
        <Button component="a" href={REPO} size="md" leftSection={<IconBrandGithub size={18} />}>Star on GitHub</Button>
        <Button component="a" href={blob('docs/design.md')} size="md" variant="default">Read the design doc</Button>
      </Group>
    </Section>
  );
}

function Footer() {
  return (
    <Box component="footer" py="xl">
      <Container size="lg">
        <Group justify="space-between">
          <Text size="sm" c="dimmed">© 2026 CHEN JIAHUI</Text>
          <Group gap="lg">
            <Anchor href={REPO} size="sm" c="dimmed">GitHub</Anchor>
            <Anchor href={blob('docs/design.md')} size="sm" c="dimmed">Design</Anchor>
            <Anchor href={blob('LICENSE')} size="sm" c="dimmed">License</Anchor>
          </Group>
        </Group>
      </Container>
    </Box>
  );
}

export function App() {
  return (
    <>
      <Header />
      <main>
        <Hero />
        <Latency />
        <Why />
        <HowItWorks />
        <Features />
        <Cli />
        <Quickstart />
        <OpenSource />
      </main>
      <Footer />
    </>
  );
}
