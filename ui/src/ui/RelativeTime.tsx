import { formatRelative, formatWhen } from "../format";

export function RelativeTime({ value }: { value?: string }) {
  if (!value) {
    return <span>Not reported</span>;
  }
  return (
    <time dateTime={value} title={formatWhen(value)}>
      {formatRelative(value)}
    </time>
  );
}
