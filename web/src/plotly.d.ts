// plotly.js-dist-min ships no type declarations. We only call newPlot, react,
// purge and Plots.resize, so declare the narrow surface we actually use.
declare module 'plotly.js-dist-min' {
  export interface PlotlyConfig {
    responsive?: boolean;
    displayModeBar?: boolean;
    [key: string]: unknown;
  }

  export interface NewPlot {
    (
      el: HTMLElement,
      data: unknown[],
      layout?: Record<string, unknown>,
      config?: PlotlyConfig,
    ): Promise<unknown>;
  }

  const Plotly: {
    newPlot: NewPlot;
    react: NewPlot;
    purge: (el: HTMLElement) => void;
    Plots: { resize: (el: HTMLElement) => void };
  };
  export default Plotly;
}
