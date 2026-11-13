window.OieRuntime.register('location-map', life => {
  const dialog = document.getElementById('oie-location-draft');
  if (!dialog) return;

  const form = dialog.querySelector('[data-location-draft]');
  const region = form.querySelector('[data-location-map]');
  const canvas = region.querySelector('[data-location-map-canvas]');
  const status = region.querySelector('[data-location-map-status]');
  const centerButton = form.querySelector('[data-location-center]');
  const latitude = form.elements.namedItem('latitude');
  const longitude = form.elements.namedItem('longitude');
  const styleURL = region.dataset.styleUrl;
  const assets = '/ui/location_map/';
  const bounds = [[-46.662, -23.564], [-46.649, -23.551]];
  const center = [-46.6562, -23.5581];
  const mercatorLimit = 85.051129;
  let map, marker, generation = 0, timeout;
  let activeLoad = false;

  function statusChange(state) {
    region.dataset.locationMapState = state;
    status.textContent = status.dataset[state] || '';
    centerButton.disabled = state !== 'ready';
    if (state !== 'loading') clearTimeout(timeout);
  }

  function close() {
    generation++;
    activeLoad = false;
    clearTimeout(timeout);
    marker?.remove();
    map?.remove();
    marker = map = undefined;
  }

  function showPoint(recenter) {
    if (!map) return;
    const point = [longitude.valueAsNumber, latitude.valueAsNumber];
    const [lng, lat] = point;
    const inside = styleURL || (lng >= bounds[0][0] && lng <= bounds[1][0] && lat >= bounds[0][1] && lat <= bounds[1][1]);
    if (!inside || !longitude.validity.valid || !latitude.validity.valid || Math.abs(lat) > mercatorLimit) {
      marker.remove();
      return;
    }
    marker.setLngLat(point).addTo(map);
    if (recenter) map.jumpTo({center:point});
  }

  function mark({lat, lng}) {
    if (!map || region.dataset.locationMapState !== 'ready') return;
    const normalized = ((lng + 180) % 360 + 360) % 360 - 180;
    latitude.value = lat.toFixed(6);
    longitude.value = normalized.toFixed(6);
    latitude.dispatchEvent(new Event('input', {bubbles:true}));
  }

  async function open() {
    const current = ++generation;
    activeLoad = true;
    statusChange('loading');
    timeout = life.timeout(() => {
      close();
      statusChange('unavailable');
    }, 15000);
    try {
      const [{Map: MapLibre, Marker, NavigationControl, setWorkerCount}, response] = await Promise.all([
        import('/ui/location_map/maplibre-gl.mjs'),
        styleURL ? null : life.fetch(assets + 'paulista.geojson'),
        loadMapStylesheet(),
      ]);
      if (current !== generation || !dialog.open || life.signal.aborted) return;
      if (response && !response.ok) throw Error('map data unavailable');
      const data = response ? await response.json() : null;
      if (current !== generation || !dialog.open || life.signal.aborted) return;
      setWorkerCount(1);
      map = new MapLibre({
        container:canvas,
        style:styleURL || localMapStyle(data),
        center, zoom:15.2, maxZoom:19, maxBounds:styleURL ? undefined : bounds,
        attributionControl:false, renderWorldCopies:false,
        dragRotate:false, pitchWithRotate:false, maxPitch:0, touchPitch:false,
        keyboard:true,
        locale:{
          'Map.Title':form.dataset.mapLabel,
          'NavigationControl.ZoomIn':form.dataset.zoomIn,
          'NavigationControl.ZoomOut':form.dataset.zoomOut,
        },
        transformRequest:url => ({url, credentials:'omit', referrerPolicy:'origin'}),
      });
      map.touchZoomRotate.disableRotation();
      map.addControl(new NavigationControl({showCompass:false}), 'top-right');
      const pin = document.createElement('span');
      pin.className = 'oie-location-map-pin';
      pin.setAttribute('aria-hidden', 'true');
      marker = new Marker({element:pin});
      map.on('click', event => mark(event.lngLat));
      map.on('load', () => {
        statusChange('ready');
        showPoint(true);
      });
    } catch {
      if (current !== generation || !dialog.open || life.signal.aborted) return;
      close();
      statusChange('unavailable');
    }
  }

  const observer = new MutationObserver(() => {
    if (dialog.open && !activeLoad) void open();
    if (!dialog.open && activeLoad) close();
  });
  observer.observe(dialog, {attributes:true, attributeFilter:['open']});
  life.cleanup(() => { observer.disconnect(); close(); });
  life.on(centerButton, 'click', () => { if (map) mark(map.getCenter()); });
  life.on(form, 'input', event => {
    if (event.target === latitude || event.target === longitude) showPoint(false);
  });
  life.on(form, 'oie:location-position', () => showPoint(true));
});

let mapStylesheet;
function loadMapStylesheet() {
  if (!mapStylesheet) {
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = '/ui/location_map/maplibre-gl.css';
    mapStylesheet = new Promise((resolve, reject) => {
      link.onload = resolve;
      link.onerror = () => {
        link.remove();
        mapStylesheet = undefined;
        reject(Error('map stylesheet unavailable'));
      };
      document.head.append(link);
    });
  }
  return mapStylesheet;
}

function localMapStyle(data) {
  const kind = name => ['==', ['get', 'kind'], name];
  const between = (low, high) => ['interpolate', ['linear'], ['zoom'], 14, low, 18, high];
  const line = (name, color, low, high, suffix = '') => ({
    id:name + suffix, type:'line', source:'osm', filter:kind(name),
    layout:{'line-cap':'round', 'line-join':'round'},
    paint:{'line-color':color, 'line-width':between(low, high)},
  });
  return {
    version:8,
    sources:{osm:{type:'geojson', data}},
    layers:[
      {id:'background', type:'background', paint:{'background-color':'#ece9df'}},
      {id:'park', type:'fill', source:'osm', filter:kind('park'),
        paint:{'fill-color':'#b7d6ae', 'fill-outline-color':'#aacaa0'}},
      {id:'building', type:'fill', source:'osm', filter:kind('building'), minzoom:15,
        paint:{'fill-color':'#dbd6cc', 'fill-outline-color':'#cec8ba'}},
      line('path', '#faf8f1', .5, 3),
      line('street', '#c6beaf', 2, 13, '-outline'),
      line('avenue', '#c3ad81', 4, 21, '-outline'),
      line('street', '#fffdfa', 1.4, 11),
      line('avenue', '#fff1c6', 3, 18),
      {id:'street-name', type:'symbol', source:'osm', minzoom:15,
        filter:['all', ['has', 'name'], ['any', kind('street'), kind('avenue')]],
        layout:{'symbol-placement':'line', 'symbol-spacing':200, 'text-field':['get', 'name'], 'text-font':['sans-serif'], 'text-size':between(10, 14)},
        paint:{'text-color':'#524a3f', 'text-halo-color':'#fffdfa', 'text-halo-width':1.5}},
      {id:'landmark', type:'symbol', source:'osm',
        filter:['all', ['has', 'name'], ['any', kind('park'), kind('building'), kind('station')]],
        layout:{'text-field':['get', 'name'], 'text-font':['sans-serif'], 'text-size':between(11, 14), 'text-max-width':12},
        paint:{'text-color':'#244b3b', 'text-halo-color':'#f7fcf1', 'text-halo-width':1.5}},
    ],
  };
}
