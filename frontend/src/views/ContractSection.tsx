import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Stack, Text } from '@primer/react'
import { AuditService, SettingsService } from '../shared/bindings'
import { downloadBlob } from '../shared/downloadBlob'
import styles from '../shared/ListCard.module.css'

export default function ContractSection() {
  const { t } = useTranslation('views')
  const [contractExportError, setContractExportError] = useState('')
  const [skillExportError, setSkillExportError] = useState('')
  const [auditExportError, setAuditExportError] = useState('')

  const exportContract = async () => {
    setContractExportError('')
    try {
      const json = await SettingsService.ExportContract()
      await downloadBlob('mill-contract.json', new Blob([json], { type: 'application/json' }))
    } catch {
      setContractExportError(t('settings.contract.exportError'))
    }
  }

  const exportSkillDoc = async () => {
    setSkillExportError('')
    try {
      const markdown = await SettingsService.ExportSkillDoc()
      await downloadBlob('mill-skill.md', new Blob([markdown], { type: 'text/markdown' }))
    } catch {
      setSkillExportError(t('settings.contract.exportSkillError'))
    }
  }

  const exportAuditTrail = async () => {
    setAuditExportError('')
    try {
      const jsonLines = await AuditService.ExportAuditTrail([])
      await downloadBlob('mill-audit-trail.jsonl', new Blob([jsonLines], { type: 'application/json' }))
    } catch {
      setAuditExportError(t('settings.contract.exportAuditError'))
    }
  }

  return (
    <>
      <Text as="p" size="small" className={styles.muted}>
        {t('settings.contract.description')}
      </Text>
      <Stack direction="horizontal" gap="condensed" align="center" style={{ marginTop: 'var(--base-size-8)' }}>
        <Button size="small" onClick={exportContract} data-testid="export-contract">
          {t('settings.contract.exportButton')}
        </Button>
      </Stack>
      {contractExportError && (
        <Text as="p" size="small" className={styles.error}>{contractExportError}</Text>
      )}
      <Text as="p" size="small" className={styles.muted} style={{ marginTop: 'var(--base-size-16)' }}>
        {t('settings.contract.exportSkillDescription')}
      </Text>
      <Stack direction="horizontal" gap="condensed" align="center" style={{ marginTop: 'var(--base-size-8)' }}>
        <Button size="small" onClick={exportSkillDoc} data-testid="export-skill-doc">
          {t('settings.contract.exportSkillButton')}
        </Button>
      </Stack>
      {skillExportError && (
        <Text as="p" size="small" className={styles.error}>{skillExportError}</Text>
      )}
      <Text as="p" size="small" className={styles.muted} style={{ marginTop: 'var(--base-size-16)' }}>
        {t('settings.contract.exportAuditDescription')}
      </Text>
      <Stack direction="horizontal" gap="condensed" align="center" style={{ marginTop: 'var(--base-size-8)' }}>
        <Button size="small" onClick={exportAuditTrail} data-testid="export-audit-trail">
          {t('settings.contract.exportAuditButton')}
        </Button>
      </Stack>
      {auditExportError && (
        <Text as="p" size="small" className={styles.error}>{auditExportError}</Text>
      )}
    </>
  )
}
