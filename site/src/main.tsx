import '@fontsource-variable/inter';
import '@mantine/core/styles.css';
import './site.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MantineProvider, createTheme } from '@mantine/core';
import { App } from './App';

const theme = createTheme({
  primaryColor: 'cyan',
  primaryShade: { light: 8, dark: 6 },
  fontFamily: 'Inter Variable, system-ui, sans-serif',
  headings: { fontFamily: 'Inter Variable, system-ui, sans-serif', fontWeight: '700' },
  defaultRadius: 'md',
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <App />
    </MantineProvider>
  </StrictMode>,
);
