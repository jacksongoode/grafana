import { css, keyframes } from '@emotion/css';
import React, { CSSProperties } from 'react';

import { useStyles2 } from '../../themes';

export interface LoadingBarProps {
  width: number;
  ariaLabel?: string;
}

const PIXELS_PER_MILLISECOND = 2.1;

export function LoadingBar({ width, ariaLabel = 'Loading bar' }: LoadingBarProps) {
  const styles = useStyles2(getStyles);
  const animationSpeed = Math.min(Math.max(Math.round(width * PIXELS_PER_MILLISECOND), 500), 4000);
  console.log('animationSpeed', animationSpeed);
  const containerStyles: CSSProperties = {
    width: '100%',
    animation: `${styles.animation} ${animationSpeed}ms infinite linear`,
    willChange: 'transform',
  };

  return (
    <div style={containerStyles}>
      <div aria-label={ariaLabel} className={styles.bar} />
    </div>
  );
}

const getStyles = () => {
  return {
    animation: keyframes({
      '0%': {
        transform: 'translateX(-50%)',
      },
      '100%': {
        transform: `translateX(100%)`,
      },
    }),
    bar: css({
      width: '28%',
      height: 1,
      background: 'linear-gradient(90deg, rgba(110, 159, 255, 0) 0%, #6E9FFF 80.75%, rgba(110, 159, 255, 0) 100%)',
    }),
  };
};
