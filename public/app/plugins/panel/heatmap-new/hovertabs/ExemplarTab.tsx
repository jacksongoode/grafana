import React from 'react';

import { ArrayVector, DataFrame, TimeZone } from '@grafana/data';

import { DataHoverView } from '../components/DataHoverView';
import { BucketLayout } from '../fields';
import { HeatmapHoverProps, HeatmapLayerHover } from '../types';
import { getHeatmapFields, timeFormatter } from '../utils';

interface HeatmapLayerOptions {
  timeZone: TimeZone;
}

export const ExemplarTab = ({
  data,
  heatmapData,
  index,
  options,
}: HeatmapHoverProps<HeatmapLayerOptions>): HeatmapLayerHover => {
  if (!heatmapData?.heatmap) {
    return {
      name: 'Exemplar',
      data: [],
    };
  }
  const [xField, yField, countField] = getHeatmapFields(heatmapData?.heatmap!);
  if (xField && yField && countField && index && index >= 0) {
    const yValueIdx = index % heatmapData?.yBucketCount! ?? 0;

    const yMinIdx = heatmapData.yLayout === BucketLayout.le ? yValueIdx - 1 : yValueIdx;
    const yMaxIdx = heatmapData.yLayout === BucketLayout.le ? yValueIdx : yValueIdx + 1;

    const xMin: number = xField.values.get(index);
    const xMax: number = xMin + heatmapData.xBucketSize!;
    const yMin: number = yField.values.get(yMinIdx);
    const yMax: number = yField.values.get(yMaxIdx);
    const count: number = countField.values.get(index);

    if (count === 0) {
      return {
        name: 'Exemplar',
        data: [],
      };
    }

    const summaryData: DataFrame = {
      name: 'Exemplar',
      fields: [
        {
          ...xField,
          config: {
            ...xField.config,
            displayNameFromDS: 'xMin',
          },
          state: {
            ...xField.state,
            displayName: 'xMin',
          },
          display: (value: number) => {
            return {
              numeric: value,
              text: timeFormatter(value, options?.timeZone!),
            };
          },
          values: new ArrayVector([xMin]),
        },
        {
          ...xField,
          config: {
            ...xField.config,
            displayNameFromDS: 'xMax',
          },
          state: {
            ...xField.state,
            displayName: 'xMax',
          },
          display: (value: number) => {
            return {
              numeric: value,
              text: timeFormatter(value, options?.timeZone!),
            };
          },
          values: new ArrayVector([xMax]),
        },
        {
          ...yField,
          config: {
            ...yField.config,
            displayNameFromDS: 'yMin',
          },
          state: {
            ...yField.state,
            displayName: 'yMin',
          },
          values: new ArrayVector([yMin]),
        },
        {
          ...yField,
          config: {
            ...yField.config,
            displayNameFromDS: 'yMax',
          },
          state: {
            ...yField.state,
            displayName: 'yMax',
          },
          values: new ArrayVector([yMax]),
        },
        {
          ...countField,
          values: new ArrayVector([count]),
        },
      ],
      length: 5,
    };

    const header = () => {
      return <DataHoverView data={summaryData} rowIndex={0} />;
    };

    return {
      name: 'Exemplar',
      header,
      data,
    };
  }

  return {
    name: 'Exemplar',
    data,
  };
};
