import { useQuery } from "@tanstack/react-query";
import { channelParams, initializationRetryInterval } from "./api";
import { readMetrics } from "./metricsApi";
import type { Channel, Connection } from "./types";

export interface ChannelTimeseriesProps {
  row: Channel;
  connection: Connection;
  keyId: string;
  window: string;
  endpoint: string;
  stream: string;
  refresh: number;
}

// All drawer curves share one precisely scoped request and one cached response.
export function useChannelTimeseries({
  row,
  connection,
  keyId,
  window,
  endpoint,
  stream,
  refresh,
}: ChannelTimeseriesProps) {
  return useQuery({
    queryKey: [
      "channel-detail-trends",
      connection.session,
      row.source_id,
      row.provider,
      row.model,
      row.upstream_model,
      keyId,
      window,
      endpoint,
      stream,
      refresh,
    ],
    queryFn: ({ signal }) => {
      const params = channelParams(keyId, window, row.model, endpoint, stream);
      params.set("provider", row.provider);
      params.set("upstream_model", row.upstream_model);
      return readMetrics(
        { ...connection, sourceId: row.source_id || connection.sourceId },
        "/v1/channel-metrics/timeseries?" + params,
        signal,
        endpoint,
        stream,
      );
    },
    retry: false,
    refetchInterval: initializationRetryInterval,
    staleTime: 15_000,
  });
}
