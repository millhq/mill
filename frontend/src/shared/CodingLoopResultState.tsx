import { useTranslation } from 'react-i18next'
import { Banner, Button, Stack, Text } from '@primer/react'
import { CopyIcon } from '@primer/octicons-react'
import type { RunDetail } from './bindings'
import { OutputViewer } from './OutputViewer'
import styles from './CodingLoopSurface.module.css'

interface Props {
  detail: RunDetail | null
  copyState: 'idle' | 'copied' | 'error'
  onCopy: () => void
}

// The Result state (docs/goals/0240 S1's design contract): full output
// per step, one-click Copy result (the M365 answer), the run saved as a
// record -- browsable afterward in Activity for free, since this is a
// real workflow run, not a bespoke exec path. Dismissal is the
// surface's own outer close control (Dialog's X/Escape for the main
// window, the panel's back arrow for Quick Panel) -- Result adds no
// second close affordance of its own, matching the design contract's
// stated action set exactly (Copy result only).
export function CodingLoopResultState({ detail, copyState, onCopy }: Props) {
  const { t } = useTranslation('app')

  if (!detail) {
    return (
      <Stack gap="condensed" align="center" data-testid="coding-loop-result-loading">
        <Text size="small">{t('codingLoop.result.loading')}</Text>
      </Stack>
    )
  }

  const succeeded = detail.status === 'SUCCESS'

  return (
    <Stack gap="condensed" className={styles.panel} data-testid="coding-loop-result">
      <Banner
        variant={succeeded ? 'success' : 'critical'}
        title={succeeded ? t('codingLoop.result.succeededTitle') : t('codingLoop.result.failedTitle')}
        description={succeeded ? undefined : detail.error}
        data-testid="coding-loop-result-banner"
      />

      <OutputViewer
        value={detail.output}
        shape="text"
        title={succeeded ? t('codingLoop.result.succeededTitle') : t('codingLoop.result.failedTitle')}
        site="coding-loop-result"
        testId="coding-loop-result-output"
      />

      <Text as="p" size="small" data-testid="coding-loop-result-saved">
        {t('codingLoop.result.saved')}
      </Text>

      {copyState === 'error' && (
        <Banner variant="critical" title={t('codingLoop.result.copyFailedTitle')} data-testid="coding-loop-result-copy-error" />
      )}

      <Stack direction="horizontal" gap="condensed" className={styles.actions}>
        <Button
          variant="primary"
          leadingVisual={CopyIcon}
          onClick={onCopy}
          data-testid="coding-loop-result-copy"
        >
          {copyState === 'copied' ? t('codingLoop.result.copied') : t('codingLoop.result.copy')}
        </Button>
      </Stack>
    </Stack>
  )
}
