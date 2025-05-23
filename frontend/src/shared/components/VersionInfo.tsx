// Libraries
import React, {FC} from 'react'
import {useSelector} from 'react-redux'

// Components
import VersionInfoOSS from 'src/shared/components/VersionInfoOSS'
import {SafeBlankLink} from 'src/utils/SafeBlankLink'

// Constants
import {VERSION, GIT_SHA, CLOUD} from 'src/shared/constants'

// Utils
import {isOrgIOx} from 'src/organizations/selectors'

const VersionInfo: FC = () => {
  const engineName = useSelector(isOrgIOx) ? 'IOx' : 'TSM'
  const engineLink = useSelector(isOrgIOx)
    ? 'https://docs.influxdata.com/influxdb/cloud-iox/'
    : 'https://docs.influxdata.com/influxdb/latest/reference/internals/storage-engine/#time-structured-merge-tree-tsm'

  return (
    <div className="version-info" data-testid="version-info">
      {CLOUD ? (
        <div>
          <SafeBlankLink href={engineLink}>
            InfluxDB Cloud powered by {engineName}
          </SafeBlankLink>
          <br />
          Version {VERSION} {GIT_SHA && <code>({GIT_SHA.slice(0, 7)})</code>}
        </div>
      ) : (
        <VersionInfoOSS />
      )}
    </div>
  )
}

export default VersionInfo
