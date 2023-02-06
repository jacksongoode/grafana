import { Subscriber, map, Observable } from 'rxjs';

import { DataQueryRequest, DataQueryResponse, dateTime, TimeRange } from '@grafana/data';
import { LoadingState } from '@grafana/schema';

import { LokiDatasource } from './datasource';
import { getRanges } from './metricTimeSplit';
import { combineResponses, resultLimitReached } from './queryUtils';
import { LokiQuery } from './types';

export function runPartitionedQuery(datasource: LokiDatasource, request: DataQueryRequest<LokiQuery>) {
  // we currently assume we are only running metric queries here.
  // for logs-queries we will have to use a different time-range-split algorithm.
  const partition: TimeRange[] = getRanges(
    request.range.from.toDate().getTime(),
    request.range.to.toDate().getTime(),
    request.intervalMs,
    60 * 1000 // we go with a hardcoded 1minute for now
  ).map(([start, end]) => {
    const from = dateTime(start);
    const to = dateTime(end);
    return {
      from,
      to,
      raw: { from, to },
    };
  });

  let mergedResponse: DataQueryResponse | null;
  const totalRequests = partition.length;

  const next = (subscriber: Subscriber<DataQueryResponse>, requestN: number) => {
    const requestId = `${request.requestId}_${requestN}`;
    const range = partition[requestN - 1];
    datasource
      .runQuery({ ...request, range, requestId })
      .pipe(
        // in case of an empty query, this is somehow run twice. `share()` is no workaround here as the observable is generated from `of()`.
        map((partialResponse) => {
          mergedResponse = combineResponses(mergedResponse, partialResponse);
          return mergedResponse;
        })
      )
      .subscribe({
        next: (response) => {
          if (requestN > 1 && resultLimitReached(request, response) === false) {
            response.state = LoadingState.Streaming;
            next(subscriber, requestN - 1);
          } else {
            response.state = LoadingState.Done;
          }

          subscriber.next(response);
        },
      });
  };

  const response = new Observable<DataQueryResponse>((subscriber) => {
    next(subscriber, totalRequests);
  });

  return response;
}
