/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
)

// Designate Condition Types used by API objects.
const (
	// DesignateRabbitMqTransportURLReadyCondition Status=True condition which indicates if the RabbitMQ TransportURLUrl is ready
	DesignateRabbitMqTransportURLReadyCondition condition.Type = "DesignateRabbitMqTransportURLReady"

	// DesignateAPIReadyCondition Status=True condition which indicates if the DesignateAPI is configured and operational
	DesignateAPIReadyCondition condition.Type = "DesignateAPIReady"

	// DesignateCentralReadyCondition Status=True condition which indicates if the DesignateCentral is configured and operational
	DesignateCentralReadyCondition condition.Type = "DesignateCentralReady"

	// DesignateSinkReadyCondition Status=True condition which indicates if the DesignateSink is configured and operational
	DesignateSinkReadyCondition condition.Type = "DesignateSinkReady"

	// DesignateWorkerReadyCondition Status=True condition which indicates if the DesignateWorker is configured and operational
	DesignateWorkerReadyCondition condition.Type = "DesignateWorkerReady"

	// DesignateMdnsReadyCondition Status=True condition which indicates if the DesignateMdns is configured and operational
	DesignateMdnsReadyCondition condition.Type = "DesignateMdnsReady"

	// DesignateProducerReadyCondition Status=True condition which indicates if the DesignateProducer is configured and operational
	DesignateProducerReadyCondition condition.Type = "DesignateProducerReady"

	// DesignateAgentReadyCondition Status=True condition which indicates if the DesignateAgent is configured and operational
	DesignateAgentReadyCondition condition.Type = "DesignateAgentReady"
)

// Designate Reasons used by API objects.
const ()

// Common Messages used by API objects.
const (
	//
	// DesignateRabbitMqTransportURLReady condition messages
	//
	// DesignateRabbitMqTransportURLReadyInitMessage
	DesignateRabbitMqTransportURLReadyInitMessage = "DesignateRabbitMqTransportURL not started"

	// DesignateRabbitMqTransportURLReadyRunningMessage
	DesignateRabbitMqTransportURLReadyRunningMessage = "DesignateRabbitMqTransportURL creation in progress"

	// DesignateRabbitMqTransportURLReadyMessage
	DesignateRabbitMqTransportURLReadyMessage = "DesignateRabbitMqTransportURL successfully created"

	// DesignateRabbitMqTransportURLReadyErrorMessage
	DesignateRabbitMqTransportURLReadyErrorMessage = "DesignateRabbitMqTransportURL error occured %s"

	//
	// DesignateAPIReady condition messages
	//
	// DesignateAPIReadyInitMessage
	DesignateAPIReadyInitMessage = "DesignateAPI not started"

	// DesignateAPIReadyErrorMessage
	DesignateAPIReadyErrorMessage = "DesignateAPI error occured %s"

	//
	// DesignateCentralReady condition messages
	//
	// DesignateCentralReadyInitMessage
	DesignateCentralReadyInitMessage = "DesignateCentral not started"

	// DesignateCentralReadyErrorMessage
	DesignateCentralReadyErrorMessage = "DesignateCentral error occured %s"

	//
	// DesignateSinkReady condition messages
	//
	// DesignateSinkReadyInitMessage
	DesignateSinkReadyInitMessage = "DesignateSink not started"

	// DesignateSinkReadyErrorMessage
	DesignateSinkReadyErrorMessage = "DesignateSink error occured %s"

	//
	// DesignateWorkerReady condition messages
	//
	// DesignateWorkerReadyInitMessage
	DesignateWorkerReadyInitMessage = "DesignateWorker not started"

	// DesignateWorkerReadyErrorMessage
	DesignateWorkerReadyErrorMessage = "DesignateWorker error occured %s"

	//
	// DesignateMdnsReady condition messages
	//
	// DesignateMdnsReadyInitMessage
	DesignateMdnsReadyInitMessage = "DesignateMdns not started"

	// DesignateMdnsReadyErrorMessage
	DesignateMdnsReadyErrorMessage = "DesignateMdns error occured %s"

	//
	// DesignateProducerReady condition messages
	//
	// DesignateProducerReadyInitMessage
	DesignateProducerReadyInitMessage = "DesignateProducer not started"

	// DesignateProducerReadyErrorMessage
	DesignateProducerReadyErrorMessage = "DesignateProducer error occured %s"

	//
	// DesignateAgentReady condition messages
	//
	// DesignateProducerReadyInitMessage
	DesignateAgentReadyInitMessage = "DesignateAgent not started"

	// DesignateProducerReadyErrorMessage
	DesignateAgentReadyErrorMessage = "DesignateAgent error occured %s"
)
