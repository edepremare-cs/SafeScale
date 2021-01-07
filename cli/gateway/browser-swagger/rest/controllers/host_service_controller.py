import connexion
import six

from rest.models.host import Host  # noqa: E501
from rest.models.host_list import HostList  # noqa: E501
from rest.models.rpc_status import RpcStatus  # noqa: E501
from rest.models.ssh_config import SshConfig  # noqa: E501
from rest import util


def host_service_create():  # noqa: E501
    """host_service_create

     # noqa: E501


    :rtype: Host
    """
    return 'do some magic!'


def host_service_delete(id, tenant_id=None, name=None):  # noqa: E501
    """host_service_delete

     # noqa: E501

    :param id: 
    :type id: str
    :param tenant_id: 
    :type tenant_id: str
    :param name: 
    :type name: str

    :rtype: object
    """
    return 'do some magic!'


def host_service_inspect(id, tenant_id=None, name=None):  # noqa: E501
    """host_service_inspect

     # noqa: E501

    :param id: 
    :type id: str
    :param tenant_id: 
    :type tenant_id: str
    :param name: 
    :type name: str

    :rtype: Host
    """
    return 'do some magic!'


def host_service_list(all=None, tenant_id=None):  # noqa: E501
    """host_service_list

     # noqa: E501

    :param all: 
    :type all: bool
    :param tenant_id: 
    :type tenant_id: str

    :rtype: HostList
    """
    return 'do some magic!'


def host_service_reboot(id):  # noqa: E501
    """host_service_reboot

     # noqa: E501

    :param id: 
    :type id: str

    :rtype: object
    """
    return 'do some magic!'


def host_service_resize(name):  # noqa: E501
    """host_service_resize

     # noqa: E501

    :param name: 
    :type name: str

    :rtype: Host
    """
    return 'do some magic!'


def host_service_ssh(id):  # noqa: E501
    """host_service_ssh

     # noqa: E501

    :param id: 
    :type id: str

    :rtype: SshConfig
    """
    return 'do some magic!'


def host_service_start(id):  # noqa: E501
    """host_service_start

     # noqa: E501

    :param id: 
    :type id: str

    :rtype: object
    """
    return 'do some magic!'


def host_service_stop(id):  # noqa: E501
    """host_service_stop

     # noqa: E501

    :param id: 
    :type id: str

    :rtype: object
    """
    return 'do some magic!'
