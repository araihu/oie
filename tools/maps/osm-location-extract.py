#!/usr/bin/env python3
"""Convert a small OpenStreetMap map API extract to the location picker's GeoJSON."""
import argparse
import json
import xml.etree.ElementTree as ET


ROADS = {"motorway", "trunk", "primary", "secondary", "tertiary",
         "residential", "unclassified", "living_street", "pedestrian",
         "service", "footway", "cycleway", "path"}


def tags(element):
    return {tag.attrib["k"]: tag.attrib["v"] for tag in element.findall("tag")}


def feature(element, kind, geometry, coordinates, name):
    return {
        "type": "Feature",
        "id": element.tag + "/" + element.attrib["id"],
        "properties": {"kind": kind, **({"name": name} if name else {})},
        "geometry": {"type": geometry, "coordinates": coordinates},
    }


def convert(root):
    nodes = {
        node.attrib["id"]: [round(float(node.attrib[key]), 6) for key in ("lon", "lat")]
        for node in root.findall("node")
    }
    features = []
    for way in root.findall("way"):
        properties = tags(way)
        coordinates = [nodes[node.attrib["ref"]] for node in way.findall("nd")]
        if len(coordinates) < 2:
            continue
        name = properties.get("name")
        road = properties.get("highway")
        if road in ROADS:
            if road in {"motorway", "trunk", "primary"}:
                kind = "avenue"
            elif road in {"service", "footway", "cycleway", "path"}:
                kind = "path"
            else:
                kind = "street"
            features.append(feature(way, kind, "LineString", coordinates, name))
            continue
        if len(coordinates) < 4 or coordinates[0] != coordinates[-1]:
            continue
        if properties.get("leisure") in {"park", "garden"}:
            features.append(feature(way, "park", "Polygon", [coordinates], name))
        elif "building" in properties:
            name = name if properties.get("tourism") == "museum" else None
            features.append(feature(way, "building", "Polygon", [coordinates], name))
    for node in root.findall("node"):
        properties = tags(node)
        if properties.get("railway") == "station" and properties.get("station") == "subway":
            features.append(feature(node, "station", "Point", nodes[node.attrib["id"]], properties.get("name")))
    bounds = root.find("bounds").attrib
    return {
        "type": "FeatureCollection",
        "bbox": [float(bounds[key]) for key in ("minlon", "minlat", "maxlon", "maxlat")],
        "attribution": "© OpenStreetMap contributors",
        "license": "https://opendatacommons.org/licenses/odbl/1-0/",
        "features": features,
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("file", help="OpenStreetMap XML extract")
    args = parser.parse_args()
    print(json.dumps(convert(ET.parse(args.file).getroot()), ensure_ascii=False, separators=(",", ":")))
