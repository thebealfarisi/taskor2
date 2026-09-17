import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Cell,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { formatTokens, type WeeklyTokenData } from "../../utils";
import { useT } from "../../../i18n";

// Mirror of DailyTokensChart's four-segment stack — same series and colours
// keep the Weekly view legible as a coarser cut of the Daily one.
export const weeklyTokenStackConfig = {
  input: { label: "Input", color: "var(--chart-1)" },
  output: { label: "Output", color: "var(--chart-2)" },
  cacheRead: { label: "Cache read", color: "var(--chart-4)" },
  cacheWrite: { label: "Cache write", color: "var(--chart-3)" },
} satisfies ChartConfig;

export function WeeklyTokensChart({ data }: { data: WeeklyTokenData[] }) {
  const { t } = useT("runtimes");

  const formatTooltipValue = (
    value: string | number | readonly (string | number)[] | undefined,
    name: string | number | undefined,
  ) => {
    const label = name == null ? "" : String(name);
    if (typeof value === "number") {
      return `${formatTokens(value)} ${label}`;
    }
    if (typeof value === "string") {
      return `${value} ${label}`;
    }
    return `${String(value ?? "")} ${label}`;
  };

  const getTooltipTotal = (
    payload: ReadonlyArray<{ value?: string | number | readonly (string | number)[] | undefined }>,
  ) => {
    let total = 0;
    for (const item of payload) {
      if (typeof item.value === "number") {
        total += item.value;
      }
    }
    return total;
  };

  return (
    <ChartContainer config={weeklyTokenStackConfig} className="aspect-[3/1] w-full">
      <BarChart data={data} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="label"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          interval="preserveStartEnd"
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          tickFormatter={(v: number) => formatTokens(v)}
          width="auto"
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelKey="rangeLabel"
              labelFormatter={(_label, payload) => {
                const row = payload[0]?.payload as WeeklyTokenData | undefined;
                if (!row) return "";
                return row.partial
                  ? t(($) => $.usage.weekly_partial_label, {
                      range: row.rangeLabel,
                      covered: row.daysCovered,
                    })
                  : row.rangeLabel;
              }}
              formatter={(value, name) => formatTooltipValue(value, name)}
              footer={(payload) => {
                const total = getTooltipTotal(payload);
                return (
                  <div className="flex items-center justify-between gap-2 font-medium">
                    <span>{t(($) => $.charts.tooltip_total)}</span>
                    <span className="font-mono tabular-nums">
                      {total.toLocaleString()}
                    </span>
                  </div>
                );
              }}
            />
          }
        />
        <Bar dataKey="input" stackId="tokens" fill="var(--color-input)">
          {data.map((d) => (
            <Cell key={d.weekStart} fillOpacity={d.partial ? 0.5 : 1} />
          ))}
        </Bar>
        <Bar dataKey="output" stackId="tokens" fill="var(--color-output)">
          {data.map((d) => (
            <Cell key={d.weekStart} fillOpacity={d.partial ? 0.5 : 1} />
          ))}
        </Bar>
        <Bar dataKey="cacheRead" stackId="tokens" fill="var(--color-cacheRead)">
          {data.map((d) => (
            <Cell key={d.weekStart} fillOpacity={d.partial ? 0.5 : 1} />
          ))}
        </Bar>
        <Bar
          dataKey="cacheWrite"
          stackId="tokens"
          fill="var(--color-cacheWrite)"
          radius={[3, 3, 0, 0]}
        >
          {data.map((d) => (
            <Cell key={d.weekStart} fillOpacity={d.partial ? 0.5 : 1} />
          ))}
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}
